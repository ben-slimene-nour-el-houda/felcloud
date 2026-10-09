package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type tickResult struct {
	ReapedCount  int
	ResumedCount int
	FailedCount  int
	PrunedEvents int
	Error        error
}

type sandboxRef struct {
	id         string
	hostname   string
	subnetName string
	privateIP  string
	status     string
	expiresAt  *int64
}

type teardownStep struct {
	name string
	run  func(db DBQueryer, s sandboxRef) error
}

func isValidTransition(from, to string) bool {
	switch from {
	case "PROVISIONING":
		return to == "ACTIVE" || to == "FAILED"
	case "ACTIVE":
		return to == "EXPIRING" || to == "TEARING_DOWN"
	case "EXPIRING":
		return to == "TEARING_DOWN"
	case "TEARING_DOWN":
		return to == "RELEASED" || to == "FAILED"
	case "FAILED":
		return to == "TEARING_DOWN"
	case "REQUESTED", "FAILED_HEALTH_CHECK", "ROLLBACK":
		return to == "TEARING_DOWN"
	default:
		return false
	}
}

func transitionSandbox(q DBQueryer, id, to string) error {
	if db, ok := q.(*sql.DB); ok {
		// Wrap in an immediate transaction for true atomicity under concurrency.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("transitionSandbox: begin tx for '%s': %w", id, err)
		}
		defer tx.Rollback()
		if err := transitionSandbox(tx, id, to); err != nil {
			return err
		}
		return tx.Commit()
	}

	var currentStatus string
	err := q.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = ?", id).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: sandbox '%s'", ErrSandboxNotFound, id)
		}
		return fmt.Errorf("failed to query sandbox '%s': %w", id, err)
	}

	if !isValidTransition(currentStatus, to) {
		return fmt.Errorf("%w for sandbox '%s': '%s' -> '%s'", ErrIllegalTransition, id, currentStatus, to)
	}

	res, err := q.Exec("UPDATE sandboxes SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ? AND status = ?", to, id, currentStatus)
	if err != nil {
		return fmt.Errorf("failed to update status for sandbox '%s': %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check affected rows for sandbox '%s': %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: sandbox '%s' (expected status '%s')", ErrTransitionConflict, id, currentStatus)
	}

	return nil
}

func reaperTick(db *sql.DB, now int64) tickResult {
	reaperTickMutex.Lock()
	defer reaperTickMutex.Unlock()
	return reaperTickInner(db, now)
}

func reaperTickInner(db *sql.DB, now int64) tickResult {
	tickStart := time.Now()
	res := tickResult{}

	// a. Reclaim expired quarantines
	reclaimExpiredQuarantinesTx(db)

	// Update oldestOverdueSeconds gauge
	var minExp sql.NullInt64
	_ = db.QueryRow("SELECT MIN(expires_at) FROM sandboxes WHERE status = 'ACTIVE' AND expires_at IS NOT NULL AND expires_at <= ?", now).Scan(&minExp)
	if minExp.Valid && minExp.Int64 <= now {
		diff := now - minExp.Int64
		if diff < 0 {
			diff = 0
		}
		oldestOverdueSeconds.Store(diff)
	} else {
		oldestOverdueSeconds.Store(0)
	}

	// b. Select up to REAPER_BATCH_SIZE sandboxes
	rows, err := db.Query(`
		SELECT sandbox_id, hostname, subnet_name, COALESCE(private_ip, ''), status, expires_at
		FROM sandboxes
		WHERE (status = 'ACTIVE' AND expires_at IS NOT NULL AND expires_at <= ?)
		   OR status IN ('EXPIRING', 'TEARING_DOWN')
		ORDER BY CASE WHEN expires_at IS NOT NULL THEN expires_at ELSE 0 END ASC, id ASC
		LIMIT ?;
	`, now, reaperBatchSize)
	if err != nil {
		res.Error = err
		return res
	}
	defer rows.Close()

	var candidates []sandboxRef
	for rows.Next() {
		var s sandboxRef
		var exp sql.NullInt64
		if err := rows.Scan(&s.id, &s.hostname, &s.subnetName, &s.privateIP, &s.status, &exp); err != nil {
			continue
		}
		if exp.Valid {
			v := exp.Int64
			s.expiresAt = &v
		}
		candidates = append(candidates, s)
	}
	rows.Close()

	// c. Process each candidate
	for _, s := range candidates {
		initialStatus := s.status
		if initialStatus == "ACTIVE" || initialStatus == "EXPIRING" {
			err := transitionSandbox(db, s.id, "TEARING_DOWN")
			if err != nil {
				if errors.Is(err, ErrTransitionConflict) || errors.Is(err, ErrIllegalTransition) || strings.Contains(err.Error(), "illegal state transition") {
					continue // another worker owns it: skip silently
				}
				log.Printf("reaper: failed transition to TEARING_DOWN for '%s': %v", s.id, err)
				continue
			}
			s.status = "TEARING_DOWN"
		}

		allStepsOK := true
		sandStart := time.Now()

		for _, step := range teardownSteps {
			stepStart := time.Now()
			stepErr := step.run(db, s)
			stepDuration := time.Since(stepStart).Milliseconds()

			if stepErr != nil {
				allStepsOK = false
				recordEvent(db, s.id, "teardown_step_failed", fmt.Sprintf("step=%s, err=%v", step.name, stepErr), stepDuration)

				teardownAttemptsMutex.Lock()
				teardownAttempts[s.id]++
				attempts := teardownAttempts[s.id]
				teardownAttemptsMutex.Unlock()

				if attempts >= teardownMaxAttempts {
					if err := transitionSandbox(db, s.id, "FAILED"); err == nil {
						recordEvent(db, s.id, "teardown_failed", fmt.Sprintf("max attempts (%d) exceeded", teardownMaxAttempts), 0)
						teardownFailures.Add(1)
						res.FailedCount++
						teardownAttemptsMutex.Lock()
						delete(teardownAttempts, s.id)
						teardownAttemptsMutex.Unlock()
					}
				}
				break // stop running further steps for this sandbox in this tick
			} else {
				recordEvent(db, s.id, "teardown_step", step.name, stepDuration)
			}
		}

		if allStepsOK {
			totalDuration := time.Since(sandStart).Milliseconds()
			if err := transitionSandbox(db, s.id, "RELEASED"); err == nil {
				recordEvent(db, s.id, "expired_teardown_done", fmt.Sprintf("duration_ms=%d", totalDuration), totalDuration)
				teardownsDone.Add(1)
				if initialStatus == "ACTIVE" {
					res.ReapedCount++
				} else {
					res.ResumedCount++
				}
				teardownAttemptsMutex.Lock()
				delete(teardownAttempts, s.id)
				teardownAttemptsMutex.Unlock()
			}
		}
	}

	// d. Prune events older than EVENTS_RETENTION_SECONDS (ts_ms) in chunks of at most 500 rows per tick
	cutoffMs := (now - int64(eventsRetentionSeconds)) * 1000
	delRes, delErr := db.Exec(`
		DELETE FROM events
		WHERE id IN (
			SELECT id FROM events WHERE ts_ms < ? ORDER BY id ASC LIMIT 500
		);
	`, cutoffMs)
	if delErr == nil {
		if n, err := delRes.RowsAffected(); err == nil && n > 0 {
			res.PrunedEvents = int(n)
			eventsPruned.Add(n)
		}
	}

	lastTickUnix.Store(now)
	lastTickDurationMs.Store(time.Since(tickStart).Milliseconds())

	return res
}

func startReaper(ctx context.Context, dbs ...*sql.DB) (<-chan struct{}, error) {
	var db *sql.DB
	var err error
	if len(dbs) > 0 && dbs[0] != nil {
		db = dbs[0]
	} else {
		db, err = getDBConnection()
		if err != nil {
			return nil, err
		}
	}
	done := make(chan struct{})
	interval := time.Duration(reaperIntervalSeconds) * time.Second
	log.Printf("Starting background reaper loop (interval: %v, batch: %d, max_attempts: %d)", interval, reaperBatchSize, teardownMaxAttempts)
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("reaper: recovered from panic: %v", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("reaper tick recovered from panic: %v", r)
						}
					}()
					reaperTick(db, nowUnix())
				}()
			}
		}
	}()
	return done, nil
}
