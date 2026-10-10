package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	dbFile := filepath.Join(tmpDir, "test_ipam.db")
	dbPath = dbFile
	defaultQuarantineSeconds = 60
	provisioner = &MockProvisioner{}
	provisionSync = provisionSyncState{}
	gatewaySyncFunc = func(bool) (bool, string) { return true, "test sync" }

	if err := initDB(); err != nil {
		t.Fatalf("failed to init test DB: %v", err)
	}
	return dbFile
}

func TestConfigGenerationBackupMigrationIsAdditiveAndIdempotent(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE config_generations; CREATE TABLE config_generations (id INTEGER PRIMARY KEY, generation_id TEXT UNIQUE NOT NULL, config_snapshot TEXT NOT NULL, status TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigGenerationBackupColumns(db); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigGenerationBackupColumns(db); err != nil {
		t.Fatalf("second migration should be safe: %v", err)
	}
	rows, err := db.Query(`PRAGMA table_info(config_generations)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if !columns["gw1_backup"] || !columns["gw2_backup"] {
		t.Fatalf("backup columns missing after migration: %#v", columns)
	}
}

func dbExecSandboxManagedBy(t *testing.T, sandboxID, owner string) (sql.Result, error) {
	t.Helper()
	db, err := getDBConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(`UPDATE sandboxes SET managed_by=? WHERE sandbox_id=?`, owner, sandboxID)
}

func TestInitDBAddsDurableTeardownColumns(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA table_info(sandboxes)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		got[name] = true
	}
	for _, name := range []string{"managed_by", "teardown_step", "teardown_attempts", "teardown_step_attempts", "teardown_started_at", "teardown_route_removed_at", "teardown_resources_deleted_at", "teardown_verified_gone_at", "teardown_released_at", "teardown_last_error"} {
		if !got[name] {
			t.Errorf("missing migration column %q", name)
		}
	}
	rows.Close()
	if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id,hostname,subnet_name,status,server_id,port_id,fixed_ip,provisioned_at,teardown_route_removed_at) VALUES('migration-check','migration-check.local','sandbox','ACTIVE','srv-check','port-check','10.20.30.99',123,456)`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSandboxesSchema(db); err != nil {
		t.Fatalf("rebuild sandbox schema: %v", err)
	}
	var serverID, portID, fixedIP string
	var provisionedAt, routeRemovedAt int64
	if err := db.QueryRow(`SELECT server_id,port_id,fixed_ip,provisioned_at,teardown_route_removed_at FROM sandboxes WHERE sandbox_id='migration-check'`).Scan(&serverID, &portID, &fixedIP, &provisionedAt, &routeRemovedAt); err != nil {
		t.Fatal(err)
	}
	if serverID != "srv-check" || portID != "port-check" || fixedIP != "10.20.30.99" || provisionedAt != 123 || routeRemovedAt != 456 {
		t.Fatalf("migration lost fields: server=%s port=%s ip=%s provisioned=%d route_removed=%d", serverID, portID, fixedIP, provisionedAt, routeRemovedAt)
	}
	var sqlText string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='sandboxes'`).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlText, "TEARDOWN_FAILED") {
		t.Error("sandbox status constraint does not include TEARDOWN_FAILED")
	}
}

func TestManagedByMigrationDefaultsExistingRowsToExternal(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id,hostname,subnet_name,private_ip,status) VALUES('legacy-owner','legacy-owner.local','sandbox','10.20.30.99','ACTIVE')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE sandboxes DROP COLUMN managed_by`); err != nil {
		db.Close()
		t.Fatalf("prepare pre-migration schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := initDB(); err != nil {
		t.Fatalf("init existing database: %v", err)
	}
	db, err = getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var managedBy string
	if err := db.QueryRow(`SELECT managed_by FROM sandboxes WHERE sandbox_id='legacy-owner'`).Scan(&managedBy); err != nil {
		t.Fatal(err)
	}
	if managedBy != "external" {
		t.Fatalf("managed_by=%q, want external", managedBy)
	}
}

func TestConcurrentAllocation(t *testing.T) {
	setupTestDB(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 5)
	ipCh := make(chan string, 5)

	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sandboxID := fmt.Sprintf("team-test-%d", id)
			res, err := allocateLease(sandboxID, "", "sandbox", "")
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d failed: %w", id, err)
				return
			}
			ipCh <- res.IPAddress
		}(i)
	}

	wg.Wait()
	close(errCh)
	close(ipCh)

	for err := range errCh {
		t.Errorf("Concurrent allocation error: %v", err)
	}

	allocatedIPs := make(map[string]bool)
	for ip := range ipCh {
		if allocatedIPs[ip] {
			t.Errorf("Duplicate IP allocated in concurrent test: %s", ip)
		}
		allocatedIPs[ip] = true
	}
	if len(allocatedIPs) != 5 {
		t.Errorf("Expected 5 distinct IPs, got %d", len(allocatedIPs))
	}
}

func TestReleaseDoesNotReuseQuarantinedIP(t *testing.T) {
	setupTestDB(t)

	res1, err := allocateLease("team-alpha", "", "sandbox", "")
	if err != nil {
		t.Fatalf("Allocation failed: %v", err)
	}
	quarantinedIP := res1.IPAddress

	_, err = releaseLease("team-alpha", quarantinedIP, 60)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	res2, err := allocateLease("team-beta", "", "sandbox", "")
	if err != nil {
		t.Fatalf("Allocation for team-beta failed: %v", err)
	}

	if res2.IPAddress == quarantinedIP {
		t.Errorf("ALLOCATION ERROR: Reused QUARANTINED IP %s for team-beta", quarantinedIP)
	}
}

func TestReclaimExpiredQuarantine(t *testing.T) {
	setupTestDB(t)

	res, err := allocateLease("team-gamma", "", "sandbox", "")
	if err != nil {
		t.Fatalf("Allocation failed: %v", err)
	}
	ip := res.IPAddress

	_, err = releaseLease("team-gamma", ip, 1)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	reclaimed := reclaimExpiredQuarantines()
	if reclaimed < 1 {
		t.Errorf("Expected at least 1 reclaimed IP, got %d", reclaimed)
	}
}

func TestAllocationUsesSubnetCIDR(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}
	_, err = db.Exec("UPDATE subnets SET cidr = '10.50.0.0/24', gateway_ip = '10.50.0.1' WHERE name = 'sandbox'")
	db.Close()
	if err != nil {
		t.Fatalf("failed to update subnet CIDR: %v", err)
	}

	res, err := allocateLease("team-custom-cidr", "", "sandbox", "")
	if err != nil {
		t.Fatalf("allocation failed for custom CIDR: %v", err)
	}

	if !strings.HasPrefix(res.IPAddress, "10.50.0.") {
		t.Errorf("Expected allocated IP to fall in 10.50.0.0/24 subnet range, got %s", res.IPAddress)
	}
}

func TestAllocationRollbackOnFailure(t *testing.T) {
	setupTestDB(t)

	// Allocate team-existing with fixed hostname
	_, err := allocateLease("team-existing", "conflict-host.local", "sandbox", "")
	if err != nil {
		t.Fatalf("initial allocation failed: %v", err)
	}

	// Try allocating team-new using identical hostname (which triggers UNIQUE constraint failure on sandboxes table)
	_, err = allocateLease("team-new", "conflict-host.local", "sandbox", "")
	if err == nil {
		t.Fatalf("expected allocation to fail due to duplicate hostname constraint")
	}

	// Verify no orphan record for team-new exists in ip_allocations table
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("db connection failed: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE entity_name = 'team-new'").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if count != 0 {
		t.Errorf("ROLLBACK FAILURE: Found %d orphan ip_allocations rows for team-new", count)
	}
}

func TestAllocationIgnoresFreeIPOutsideCIDR(t *testing.T) {
	setupTestDB(t)

	res1, err := allocateLease("team-stale", "", "sandbox", "")
	if err != nil {
		t.Fatalf("initial allocation failed: %v", err)
	}
	staleFreeIP := res1.IPAddress

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}
	_, err = db.Exec("UPDATE ip_allocations SET status = 'FREE', quarantined_at = NULL, quarantine_until = NULL WHERE ip_address = ?", staleFreeIP)
	if err != nil {
		db.Close()
		t.Fatalf("failed to set status FREE: %v", err)
	}

	_, err = db.Exec("UPDATE subnets SET cidr = '10.88.0.0/24', gateway_ip = '10.88.0.1' WHERE name = 'sandbox'")
	db.Close()
	if err != nil {
		t.Fatalf("failed to update subnet CIDR: %v", err)
	}

	res2, err := allocateLease("team-fresh", "", "sandbox", "")
	if err != nil {
		t.Fatalf("allocation failed for team-fresh: %v", err)
	}

	if res2.IPAddress == staleFreeIP {
		t.Errorf("ALLOCATION ERROR: Reused stale FREE IP %s outside of active subnet CIDR 10.88.0.0/24", staleFreeIP)
	}
	if !strings.HasPrefix(res2.IPAddress, "10.88.0.") {
		t.Errorf("Expected allocated IP to fall in 10.88.0.0/24 subnet range, got %s", res2.IPAddress)
	}
}

func TestValidateRoutingDomain(t *testing.T) {
	origDomain := routingDomain
	t.Cleanup(func() { routingDomain = origDomain })
	routingDomain = "cstam.felcloud.tn"

	tests := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{"valid standard domain", "cstam.felcloud.tn", false},
		{"valid sub-domain with hyphen", "a.b-c.example", false},
		{"valid single label", "example", false},
		{"empty domain", "", true},
		{"leading dot", ".cstam.felcloud.tn", true},
		{"trailing dot", "cstam.felcloud.tn.", true},
		{"leading hyphen", "-cstam.felcloud.tn", true},
		{"trailing hyphen", "cstam.felcloud.tn-", true},
		{"spaces in domain", "cstam .felcloud.tn", true},
		{"uppercase characters", "CSTAM.felcloud.tn", true},
		{"newline injection", "x.com\n  http-request deny", true},
		{"semicolon injection", "x.com; drop table", true},
		{"64-char label (>63 chars)", strings.Repeat("a", 64) + ".felcloud.tn", true},
		{"191-char total (>190 chars)", strings.Repeat("a.", 94) + "tnx", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRoutingDomain(tt.domain)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRoutingDomain(%q) err = %v, wantErr %v", tt.domain, err, tt.wantErr)
			}
		})
	}
}

func TestGenerateHAProxyConfigRoutingDomain(t *testing.T) {
	origDomain := routingDomain
	t.Cleanup(func() { routingDomain = origDomain })

	teams := []TeamEntry{
		{Name: "team5", IP: "10.20.30.50", Port: 80},
	}

	domains := []string{"cstam.felcloud.tn", "sandbox.custom-zone.org"}

	for _, d := range domains {
		routingDomain = d
		cfg := generateHAProxyConfig(teams)

		expectedACL := fmt.Sprintf("acl is_team5 hdr(host) -i team5.%s", d)
		if !strings.Contains(cfg, expectedACL) {
			t.Errorf("generateHAProxyConfig for domain %s missing expected ACL %q", d, expectedACL)
		}
		if strings.Contains(cfg, ".local") {
			t.Errorf("generateHAProxyConfig for domain %s contains unwanted '.local'", d)
		}
	}
}

func TestTransitionSandboxTable(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("db connection failed: %v", err)
	}
	defer db.Close()

	// Seed sandboxes in various initial states
	initialStates := []string{
		"PROVISIONING", "ACTIVE", "EXPIRING", "TEARING_DOWN", "RELEASED", "FAILED",
		"REQUESTED", "FAILED_HEALTH_CHECK", "ROLLBACK",
	}

	for _, st := range initialStates {
		_, err := db.Exec(`
			INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
			VALUES (?, ?, 'sandbox', '10.20.30.50', ?)
			ON CONFLICT(sandbox_id) DO UPDATE SET status = excluded.status;
		`, "sb-"+st, "sb-"+st+".cstam.felcloud.tn", st)
		if err != nil {
			t.Fatalf("failed to insert sandbox sb-%s: %v", st, err)
		}
	}

	targetStates := []string{
		"PROVISIONING", "ACTIVE", "EXPIRING", "TEARING_DOWN", "RELEASED", "FAILED",
		"REQUESTED", "FAILED_HEALTH_CHECK", "ROLLBACK",
	}

	validTransitions := map[string]map[string]bool{
		"PROVISIONING":        {"ACTIVE": true, "FAILED": true},
		"ACTIVE":              {"EXPIRING": true, "TEARING_DOWN": true},
		"EXPIRING":            {"TEARING_DOWN": true},
		"TEARING_DOWN":        {"RELEASED": true, "FAILED": true},
		"FAILED":              {"TEARING_DOWN": true},
		"REQUESTED":           {"TEARING_DOWN": true},
		"FAILED_HEALTH_CHECK": {"TEARING_DOWN": true},
		"ROLLBACK":            {"TEARING_DOWN": true},
	}

	for _, from := range initialStates {
		for _, to := range targetStates {
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				// Reset status for this sandbox
				sbID := "sb-" + from
				_, err := db.Exec("UPDATE sandboxes SET status = ? WHERE sandbox_id = ?", from, sbID)
				if err != nil {
					t.Fatalf("failed to reset sandbox status: %v", err)
				}

				wantAllowed := validTransitions[from][to]
				err = transitionSandbox(db, sbID, to)
				if (err == nil) != wantAllowed {
					t.Errorf("transitionSandbox(%s -> %s) err = %v, wantAllowed = %v", from, to, err, wantAllowed)
				}
				if wantAllowed {
					var current string
					_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&current)
					if current != to {
						t.Errorf("expected DB status to be %s, got %s", to, current)
					}
				}
			})
		}
	}
}

func TestTransitionSandboxConcurrency(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("db connection failed: %v", err)
	}
	defer db.Close()

	// Create an ACTIVE sandbox
	sbID := "race-sandbox"
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES (?, ?, 'sandbox', '10.20.30.90', 'ACTIVE')
		ON CONFLICT(sandbox_id) DO UPDATE SET status = 'ACTIVE';
	`, sbID, sbID+".cstam.felcloud.tn")
	if err != nil {
		t.Fatalf("failed to insert race sandbox: %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex
	errCount := 0

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, errConn := getDBConnection()
			if errConn != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}
			defer conn.Close()

			err := transitionSandbox(conn, sbID, "TEARING_DOWN")
			mu.Lock()
			if err == nil {
				successCount++
			} else {
				errCount++
				if !errors.Is(err, ErrTransitionConflict) && !strings.Contains(err.Error(), "illegal state transition") {
					t.Errorf("unexpected error in goroutine: %v", err)
				}
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful transition, got %d", successCount)
	}
	if errCount != concurrency-1 {
		t.Errorf("Expected exactly %d failed transitions, got %d", concurrency-1, errCount)
	}
}

func TestEventsMigrationIdempotency(t *testing.T) {
	setupTestDB(t)

	// Call initDB a second time on initialized database
	if err := initDB(); err != nil {
		t.Fatalf("second initDB failed: %v", err)
	}

	// Record an event
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("db connection failed: %v", err)
	}
	defer db.Close()

	recordEvent(db, "test-sb", "allocated", "ip=10.20.30.55", 12)

	// Call initDB a third time
	if err := initDB(); err != nil {
		t.Fatalf("third initDB failed: %v", err)
	}

	// Verify the existing event row is intact
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = 'test-sb' AND type = 'allocated'").Scan(&count)
	if err != nil {
		t.Fatalf("querying events failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 event row preserved across initDB calls, got %d", count)
	}
}

func TestEventsRecordingAndAPI(t *testing.T) {
	setupTestDB(t)

	// Perform an allocation
	allocRes, err := allocateLease("team-ev-test", "", "sandbox", "")
	if err != nil {
		t.Fatalf("allocateLease failed: %v", err)
	}

	// Perform a release
	_, err = releaseLease("team-ev-test", allocRes.IPAddress, 30)
	if err != nil {
		t.Fatalf("releaseLease failed: %v", err)
	}

	// Verify events in DB
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("db connection failed: %v", err)
	}
	defer db.Close()

	var allocCount, relCount int
	var allocTs, relTs int64
	err = db.QueryRow("SELECT COUNT(*), ts_ms FROM events WHERE sandbox_id = 'team-ev-test' AND type = 'allocated'").Scan(&allocCount, &allocTs)
	if err != nil || allocCount != 1 {
		t.Fatalf("expected 1 allocated event, got count %d, err %v", allocCount, err)
	}

	err = db.QueryRow("SELECT COUNT(*), ts_ms FROM events WHERE sandbox_id = 'team-ev-test' AND type = 'released'").Scan(&relCount, &relTs)
	if err != nil || relCount != 1 {
		t.Fatalf("expected 1 released event, got count %d, err %v", relCount, err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	if allocTs <= 0 || allocTs > nowMs || relTs <= 0 || relTs > nowMs {
		t.Errorf("implausible event timestamps: allocTs=%d, relTs=%d, nowMs=%d", allocTs, relTs, nowMs)
	}

	// Test GET /api/events with httptest
	req := httptest.NewRequest("GET", "/api/events?limit=1", nil)
	rec := httptest.NewRecorder()
	handler := setupAPIRoutes()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/events returned code %d: %s", rec.Code, rec.Body.String())
	}

	var events []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected limit=1 event, got %d", len(events))
	}

	// Limit=1 should return the newest event ("released")
	if events[0]["type"] != "released" {
		t.Errorf("expected newest event type 'released', got '%v'", events[0]["type"])
	}
}

type staleQueryer struct {
	tx   *sql.Tx
	db   *sql.DB
	sbID string
}

func (s *staleQueryer) Exec(query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "WHERE sandbox_id = ? AND status = ?") {
		// Mutate status to TEARING_DOWN right before the guarded UPDATE runs
		_, _ = s.tx.Exec("UPDATE sandboxes SET status = 'TEARING_DOWN' WHERE sandbox_id = ?", s.sbID)
	}
	return s.tx.Exec(query, args...)
}

func (s *staleQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	return s.tx.Query(query, args...)
}

func (s *staleQueryer) QueryRow(query string, args ...any) *sql.Row {
	return s.tx.QueryRow(query, args...)
}

func TestErrTransitionConflict(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	sbID := "conflict-sb"
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES (?, ?, 'sandbox', '10.20.30.95', 'ACTIVE')
	`, sbID, sbID+".cstam.felcloud.tn")
	if err != nil {
		t.Fatalf("failed to insert test sandbox: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx failed: %v", err)
	}
	defer tx.Rollback()

	sq := &staleQueryer{tx: tx, db: db, sbID: sbID}
	err = transitionSandbox(sq, sbID, "EXPIRING")
	if err == nil {
		t.Fatalf("expected error from transitionSandbox, got nil")
	}
	if !errors.Is(err, ErrTransitionConflict) {
		t.Errorf("expected errors.Is(err, ErrTransitionConflict), got %v", err)
	}
}

func TestRecordEventBestEffort(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("DROP TABLE events"); err != nil {
		t.Fatalf("failed to drop events table: %v", err)
	}

	allocRes, err := allocateLease("team-noevents", "", "sandbox", "")
	if err != nil {
		t.Fatalf("allocateLease failed when events table is missing: %v", err)
	}
	if allocRes == nil || allocRes.IPAddress == "" {
		t.Fatalf("expected valid LeaseResponse, got nil/empty IP")
	}

	var sbCount int
	err = db.QueryRow("SELECT COUNT(*) FROM sandboxes WHERE sandbox_id = 'team-noevents'").Scan(&sbCount)
	if err != nil || sbCount != 1 {
		t.Fatalf("expected sandbox row present, got count=%d, err=%v", sbCount, err)
	}

	relRes, err := releaseLease("team-noevents", allocRes.IPAddress, 30)
	if err != nil {
		t.Fatalf("releaseLease failed when events table is missing: %v", err)
	}
	if relRes == nil || relRes.Status != "QUARANTINED" {
		t.Fatalf("expected release status QUARANTINED, got %v", relRes)
	}

	var allocStatus string
	err = db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = ?", allocRes.IPAddress).Scan(&allocStatus)
	if err != nil || allocStatus != "QUARANTINED" {
		t.Fatalf("expected allocation status QUARANTINED, got %s, err=%v", allocStatus, err)
	}
}

func TestSandboxesSchemaMigration(t *testing.T) {
	tmpDB := filepath.Join(t.TempDir(), "migration_test.db")
	dbMutex.Lock()
	oldDBPath := dbPath
	dbPath = tmpDB
	dbMutex.Unlock()
	t.Cleanup(func() {
		dbMutex.Lock()
		dbPath = oldDBPath
		dbMutex.Unlock()
	})

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	// 1. Build legacy subnets and sandboxes tables (without EXPIRING / TEARING_DOWN)
	_, err = db.Exec(`
		CREATE TABLE subnets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			cidr TEXT NOT NULL,
			gateway_ip TEXT NOT NULL,
			description TEXT
		);
		INSERT INTO subnets (name, cidr, gateway_ip) VALUES ('sandbox', '10.20.30.0/24', '10.20.30.1');

		CREATE TABLE sandboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sandbox_id TEXT UNIQUE NOT NULL,
			hostname TEXT UNIQUE NOT NULL,
			subnet_name TEXT NOT NULL,
			private_ip TEXT,
			status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'FAILED', 'FAILED_HEALTH_CHECK', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(subnet_name) REFERENCES subnets(name)
		);
	`)
	if err != nil {
		t.Fatalf("failed to create legacy schema: %v", err)
	}

	legacyStatuses := []string{
		"REQUESTED", "PROVISIONING", "ACTIVE", "FAILED", "FAILED_HEALTH_CHECK", "RELEASED", "ROLLBACK",
	}

	type fullSandboxRow struct {
		id        int
		sandboxID string
		hostname  string
		subnet    string
		privateIP string
		status    string
		createdAt string
		updatedAt string
	}

	var beforeRows []fullSandboxRow

	for i, st := range legacyStatuses {
		sID := fmt.Sprintf("sb-%d", i)
		host := fmt.Sprintf("host-%d.cstam.felcloud.tn", i)
		ip := fmt.Sprintf("10.20.30.%d", 10+i)
		_, err := db.Exec(`
			INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
			VALUES (?, ?, 'sandbox', ?, ?)
		`, sID, host, ip, st)
		if err != nil {
			t.Fatalf("failed to insert legacy sandbox %s: %v", st, err)
		}
	}

	// Query pre-migration schema objects
	masterBefore, _ := getMasterObjects(db, "sandboxes")
	t.Logf("sqlite_master sandboxes objects BEFORE migration: %v", masterBefore)

	// Query pre-migration rows (all columns)
	rBefore, err := db.Query("SELECT id, sandbox_id, hostname, subnet_name, COALESCE(private_ip,''), status, created_at, updated_at FROM sandboxes ORDER BY id ASC")
	if err != nil {
		t.Fatalf("failed to query pre-migration sandboxes: %v", err)
	}
	for rBefore.Next() {
		var row fullSandboxRow
		if err := rBefore.Scan(&row.id, &row.sandboxID, &row.hostname, &row.subnet, &row.privateIP, &row.status, &row.createdAt, &row.updatedAt); err != nil {
			t.Fatalf("scan pre-migration row failed: %v", err)
		}
		beforeRows = append(beforeRows, row)
	}
	rBefore.Close()

	// 2. Run initDB to execute migration
	if err := initDB(); err != nil {
		t.Fatalf("initDB migration failed: %v", err)
	}

	// Query post-migration schema objects
	masterAfter, _ := getMasterObjects(db, "sandboxes")
	t.Logf("sqlite_master sandboxes objects AFTER migration: %v", masterAfter)

	// Check lost indexes/triggers
	if len(masterAfter) < len(masterBefore) {
		t.Errorf("lost schema objects! before=%v after=%v", masterBefore, masterAfter)
	}

	// 3. Assert all rows survived with identical values across ALL columns
	rAfter, err := db.Query("SELECT id, sandbox_id, hostname, subnet_name, COALESCE(private_ip,''), status, created_at, updated_at FROM sandboxes ORDER BY id ASC")
	if err != nil {
		t.Fatalf("failed to query post-migration sandboxes: %v", err)
	}
	defer rAfter.Close()

	count := 0
	for rAfter.Next() {
		var row fullSandboxRow
		if err := rAfter.Scan(&row.id, &row.sandboxID, &row.hostname, &row.subnet, &row.privateIP, &row.status, &row.createdAt, &row.updatedAt); err != nil {
			t.Fatalf("scan post-migration row failed: %v", err)
		}
		exp := beforeRows[count]
		if row != exp {
			t.Errorf("row %d mismatch:\n got: %+v\nwant: %+v", count, row, exp)
		}
		count++
	}
	if count != len(legacyStatuses) {
		t.Fatalf("expected %d rows surviving, got %d", len(legacyStatuses), count)
	}

	// 4. Assert a second initDB is a no-op
	if err := initDB(); err != nil {
		t.Fatalf("second initDB failed: %v", err)
	}

	// Assert EXPIRING status can now be inserted
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES ('sb-exp', 'host-exp.cstam.felcloud.tn', 'sandbox', '10.20.30.99', 'EXPIRING')
	`)
	if err != nil {
		t.Fatalf("failed to insert EXPIRING sandbox after migration: %v", err)
	}
}

func getMasterObjects(db *sql.DB, tblName string) ([]string, error) {
	rows, err := db.Query("SELECT type || ':' || name FROM sqlite_master WHERE tbl_name = ? ORDER BY name ASC", tblName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			res = append(res, name)
		}
	}
	return res, nil
}

func TestAllocationTTL(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	oldNow := nowUnix
	nowUnix = func() int64 { return baseTime }
	t.Cleanup(func() { nowUnix = oldNow })

	// 1. Default TTL
	res, err := allocateLease("sb-def", "", "sandbox", "")
	if err != nil {
		t.Fatalf("allocateLease failed: %v", err)
	}
	if res.ExpiresAt == nil || *res.ExpiresAt != baseTime+14400 {
		t.Fatalf("expected expires_at %d, got %v", baseTime+14400, res.ExpiresAt)
	}

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	var dbExp int64
	err = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = 'sb-def'").Scan(&dbExp)
	if err != nil || dbExp != baseTime+14400 {
		t.Fatalf("db expires_at mismatch: err=%v, exp=%d", err, dbExp)
	}

	// 2. Custom TTL honored
	resCust, err := allocateLeaseTTL("sb-custom", "", "sandbox", "", 7200)
	if err != nil {
		t.Fatalf("allocateLeaseTTL failed: %v", err)
	}
	if resCust.ExpiresAt == nil || *resCust.ExpiresAt != baseTime+7200 {
		t.Fatalf("expected custom expires_at %d, got %v", baseTime+7200, resCust.ExpiresAt)
	}

	// 3. TTL above cap rejected
	_, err = allocateLeaseTTL("sb-overcap", "", "sandbox", "", 90000)
	if err == nil {
		t.Fatalf("expected error for TTL > maxTTLSeconds, got nil")
	}

	// 4. Rolled-back allocation leaves no row
	_, err = allocateLeaseTTL("sb-badip", "", "sandbox", "10.20.30.255", 3600)
	if err == nil {
		t.Fatalf("expected error for invalid preferred IP, got nil")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes WHERE sandbox_id = 'sb-badip'").Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 sandboxes rows for rolled back allocation, got %d", count)
	}
}

func TestExtendSandbox(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	// Seed an ACTIVE sandbox with expires_at = baseTime + 3600
	sbID := "sb-ext-test"
	initialExp := baseTime + 3600
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at)
		VALUES (?, ?, 'sandbox', '10.20.30.60', 'ACTIVE', ?)
	`, sbID, sbID+".cstam.felcloud.tn", initialExp)
	if err != nil {
		t.Fatalf("failed to insert test sandbox: %v", err)
	}

	// 1. Success adds exactly extend_seconds
	newExp, err := extendSandbox(db, sbID, 1800)
	if err != nil {
		t.Fatalf("extendSandbox failed: %v", err)
	}
	if newExp != initialExp+1800 {
		t.Fatalf("expected newExp %d, got %d", initialExp+1800, newExp)
	}

	// 2. Extending when now > old expiry (uses now + extend)
	currTime = baseTime + 6000 // now is after current expiry (initialExp+1800 = baseTime+5400)
	newExp2, err := extendSandbox(db, sbID, 1800)
	if err != nil {
		t.Fatalf("extendSandbox after expiry failed: %v", err)
	}
	if newExp2 != currTime+1800 {
		t.Fatalf("expected newExp2 %d (currTime+1800), got %d", currTime+1800, newExp2)
	}

	// 3. Cap exceeded -> ErrTTLCapExceeded and expires_at unchanged
	currTime = baseTime
	var expBeforeCap int64
	_ = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&expBeforeCap)
	_, err = extendSandbox(db, sbID, 86400) // 86400 from expBeforeCap exceeds maxTTLSeconds (86400 from now)
	if !errors.Is(err, ErrTTLCapExceeded) {
		t.Fatalf("expected ErrTTLCapExceeded, got %v", err)
	}
	var expAfterCap int64
	_ = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&expAfterCap)
	if expAfterCap != expBeforeCap {
		t.Fatalf("expected expires_at unchanged after cap rejection: before=%d, after=%d", expBeforeCap, expAfterCap)
	}

	// 4. Too small (< 60s) -> error
	_, err = extendSandbox(db, sbID, 30)
	if err == nil {
		t.Fatalf("expected error for extend < minExtendSeconds, got nil")
	}

	// 5. Non-ACTIVE states (FAILED, EXPIRING) -> rejected with ErrWrongState
	_, err = db.Exec("UPDATE sandboxes SET status = 'FAILED' WHERE sandbox_id = ?", sbID)
	if err != nil {
		t.Fatalf("update to FAILED failed: %v", err)
	}
	_, err = extendSandbox(db, sbID, 600)
	if !errors.Is(err, ErrWrongState) {
		t.Fatalf("expected ErrWrongState for FAILED sandbox, got %v", err)
	}

	_, err = db.Exec("UPDATE sandboxes SET status = 'EXPIRING' WHERE sandbox_id = ?", sbID)
	if err != nil {
		t.Fatalf("update to EXPIRING failed: %v", err)
	}
	_, err = extendSandbox(db, sbID, 600)
	if !errors.Is(err, ErrWrongState) {
		t.Fatalf("expected ErrWrongState for EXPIRING sandbox, got %v", err)
	}

	// 6. Unknown sandbox
	_, err = extendSandbox(db, "non-existent-sb", 600)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error for unknown sandbox, got %v", err)
	}

	// 7. NULL expires_at
	_, err = db.Exec("UPDATE sandboxes SET status = 'ACTIVE', expires_at = NULL WHERE sandbox_id = ?", sbID)
	if err != nil {
		t.Fatalf("update to NULL expires_at failed: %v", err)
	}
	_, err = extendSandbox(db, sbID, 600)
	if err == nil {
		t.Fatalf("expected error for NULL expires_at sandbox, got nil")
	}
}

func TestExtendSandboxConcurrency(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	oldNow := nowUnix
	nowUnix = func() int64 { return baseTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	sbID := "race-ext-sb"
	initialExp := baseTime + 3600
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at)
		VALUES (?, ?, 'sandbox', '10.20.30.65', 'ACTIVE', ?)
	`, sbID, sbID+".cstam.felcloud.tn", initialExp)
	if err != nil {
		t.Fatalf("failed to insert race sandbox: %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, errConn := getDBConnection()
			if errConn != nil {
				errCh <- errConn
				return
			}
			defer conn.Close()

			_, errExt := extendSandbox(conn, sbID, 600)
			if errExt != nil {
				errCh <- errExt
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for errExt := range errCh {
		t.Errorf("concurrent extend failed: %v", errExt)
	}

	var finalExp int64
	err = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&finalExp)
	if err != nil {
		t.Fatalf("query final expires_at failed: %v", err)
	}

	expectedExp := initialExp + int64(concurrency)*600
	if finalExp != expectedExp {
		t.Fatalf("final expires_at mismatch: got %d, want %d", finalExp, expectedExp)
	}
}

func TestExtendAPI(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	oldNow := nowUnix
	nowUnix = func() int64 { return baseTime }
	t.Cleanup(func() { nowUnix = oldNow })

	handler := setupAPIRoutes()

	// Provision active sandbox with TTL
	allocRes, err := allocateLeaseTTL("api-ext-sb", "", "sandbox", "", 3600)
	if err != nil {
		t.Fatalf("allocateLeaseTTL failed: %v", err)
	}
	if allocRes.ExpiresAt == nil {
		t.Fatalf("expected non-nil expires_at")
	}

	// 1. 200 OK
	body := `{"sandbox_id": "api-ext-sb", "extend_seconds": 1200}`
	req := httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["success"] != true || resp["expires_at"] != float64(baseTime+4800) {
		t.Fatalf("unexpected 200 response: %v", resp)
	}

	// 2. 400 Bad Request - Unknown field
	bodyBadField := `{"sandbox_id": "api-ext-sb", "extend_seconds": 1200, "extra": 123}`
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(bodyBadField))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. 400 Bad Request - Malformed JSON
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{bad json`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed json, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. 400 Bad Request - Invalid sandbox_id format
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{"sandbox_id": "-invalid", "extend_seconds": 600}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid sandbox_id, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. 400 Bad Request - extend_seconds < 60
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{"sandbox_id": "api-ext-sb", "extend_seconds": 30}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for extend_seconds < 60, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. 400 Bad Request - Oversized body (> 4 KB)
	hugeString := strings.Repeat("a", 5000)
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(fmt.Sprintf(`{"sandbox_id": "%s", "extend_seconds": 600}`, hugeString)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. 404 Not Found - unknown sandbox
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{"sandbox_id": "unknown-sb", "extend_seconds": 600}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown sandbox, got %d: %s", rec.Code, rec.Body.String())
	}

	// 8. 422 Unprocessable Entity - cap exceeded
	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{"sandbox_id": "api-ext-sb", "extend_seconds": 86400}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 422 {
		t.Fatalf("expected 422 for cap exceeded, got %d: %s", rec.Code, rec.Body.String())
	}

	// 9. 409 Conflict - non-ACTIVE or NULL expires_at
	db, _ := getDBConnection()
	db.Exec("UPDATE sandboxes SET expires_at = NULL WHERE sandbox_id = 'api-ext-sb'")
	db.Close()

	req = httptest.NewRequest("POST", "/api/sandboxes/extend", bytes.NewBufferString(`{"sandbox_id": "api-ext-sb", "extend_seconds": 600}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for NULL expires_at, got %d: %s", rec.Code, rec.Body.String())
	}

	// 10. GET /api/sandboxes checks
	req = httptest.NewRequest("GET", "/api/sandboxes", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sandboxes returned %d", rec.Code)
	}
	var listResp map[string][]map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	sbs := listResp["sandboxes"]
	if len(sbs) == 0 {
		t.Fatalf("expected at least 1 sandbox in GET /api/sandboxes")
	}
	found := false
	for _, sb := range sbs {
		if sb["sandbox_id"] == "api-ext-sb" {
			found = true
			if sb["expires_at"] != nil || sb["ttl_remaining_seconds"] != nil {
				t.Fatalf("expected null expires_at and ttl_remaining_seconds for NULL expires_at sandbox")
			}
		}
	}
	if !found {
		t.Fatalf("api-ext-sb not found in GET /api/sandboxes response")
	}
}

func TestSandboxesSchemaMigrationTTL(t *testing.T) {
	tmpDB := filepath.Join(t.TempDir(), "migration_ttl_test.db")
	dbMutex.Lock()
	oldDBPath := dbPath
	dbPath = tmpDB
	dbMutex.Unlock()
	t.Cleanup(func() {
		dbMutex.Lock()
		dbPath = oldDBPath
		dbMutex.Unlock()
	})

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	// 1. Build legacy subnets and sandboxes tables (without expires_at and with legacy CHECK)
	_, err = db.Exec(`
		CREATE TABLE subnets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			cidr TEXT NOT NULL,
			gateway_ip TEXT NOT NULL,
			description TEXT
		);
		INSERT INTO subnets (name, cidr, gateway_ip) VALUES ('sandbox', '10.20.30.0/24', '10.20.30.1');

		CREATE TABLE sandboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sandbox_id TEXT UNIQUE NOT NULL,
			hostname TEXT UNIQUE NOT NULL,
			subnet_name TEXT NOT NULL,
			private_ip TEXT,
			status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'FAILED', 'FAILED_HEALTH_CHECK', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(subnet_name) REFERENCES subnets(name)
		);

		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES ('legacy-1', 'legacy-1.cstam.felcloud.tn', 'sandbox', '10.20.30.10', 'ACTIVE');
	`)
	if err != nil {
		t.Fatalf("failed to create legacy schema: %v", err)
	}

	// 2. Run initDB to execute both migrations
	if err := initDB(); err != nil {
		t.Fatalf("initDB failed on legacy schema: %v", err)
	}

	// 3. Verify expires_at column exists
	var hasExpiresAt bool
	infoRows, err := db.Query("PRAGMA table_info(sandboxes)")
	if err != nil {
		t.Fatalf("pragma table_info failed: %v", err)
	}
	for infoRows.Next() {
		var cid int
		var name, typeStr string
		var notNull int
		var dfltValue interface{}
		var pk int
		if err := infoRows.Scan(&cid, &name, &typeStr, &notNull, &dfltValue, &pk); err == nil {
			if name == "expires_at" {
				hasExpiresAt = true
			}
		}
	}
	infoRows.Close()

	if !hasExpiresAt {
		t.Fatalf("expires_at column missing after migration")
	}

	// 4. Verify legacy row has expires_at IS NULL
	var expVal sql.NullInt64
	var status string
	err = db.QueryRow("SELECT status, expires_at FROM sandboxes WHERE sandbox_id = 'legacy-1'").Scan(&status, &expVal)
	if err != nil {
		t.Fatalf("query legacy row failed: %v", err)
	}
	if expVal.Valid {
		t.Fatalf("expected legacy row to have NULL expires_at, got %d", expVal.Int64)
	}

	// 5. Verify EXPIRING status is accepted (CHECK constraint migrated)
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at)
		VALUES ('legacy-exp', 'legacy-exp.cstam.felcloud.tn', 'sandbox', '10.20.30.11', 'EXPIRING', 1700000000)
	`)
	if err != nil {
		t.Fatalf("failed to insert EXPIRING sandbox with expires_at: %v", err)
	}

	// 6. Run second initDB - assert idempotent no-op
	if err := initDB(); err != nil {
		t.Fatalf("second initDB failed: %v", err)
	}
}

func TestExpiresAtMigrationFromS2Schema(t *testing.T) {
	tmpDB := filepath.Join(t.TempDir(), "s2_migration_test.db")
	dbMutex.Lock()
	oldDBPath := dbPath
	dbPath = tmpDB
	dbMutex.Unlock()
	t.Cleanup(func() {
		dbMutex.Lock()
		dbPath = oldDBPath
		dbMutex.Unlock()
	})

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer db.Close()

	// 1. Build S2 schema: sandboxes table with expanded CHECK constraint (includes EXPIRING & TEARING_DOWN) but NO expires_at column
	_, err = db.Exec(`
		CREATE TABLE subnets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			cidr TEXT NOT NULL,
			gateway_ip TEXT NOT NULL,
			description TEXT
		);
		INSERT INTO subnets (name, cidr, gateway_ip) VALUES ('sandbox', '10.20.30.0/24', '10.20.30.1');

		CREATE TABLE ip_allocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ip_address TEXT UNIQUE NOT NULL,
			subnet_name TEXT NOT NULL,
			entity_name TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			status TEXT CHECK(status IN ('ALLOCATED', 'RESERVED', 'FREE', 'RELEASED', 'QUARANTINED')) DEFAULT 'ALLOCATED',
			quarantined_at TIMESTAMP,
			quarantine_until TIMESTAMP,
			assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(subnet_name) REFERENCES subnets(name)
		);

		CREATE TABLE sandboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sandbox_id TEXT UNIQUE NOT NULL,
			hostname TEXT UNIQUE NOT NULL,
			subnet_name TEXT NOT NULL,
			private_ip TEXT,
			status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'EXPIRING', 'TEARING_DOWN', 'FAILED', 'FAILED_HEALTH_CHECK', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(subnet_name) REFERENCES subnets(name)
		);

		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES ('s2-sb-1', 's2-sb-1.cstam.felcloud.tn', 'sandbox', '10.20.30.51', 'ACTIVE');
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES ('s2-sb-2', 's2-sb-2.cstam.felcloud.tn', 'sandbox', '10.20.30.52', 'EXPIRING');
	`)
	if err != nil {
		t.Fatalf("failed to create S2 schema: %v", err)
	}

	// 2. Run initDB to execute additive expires_at migration
	if err := initDB(); err != nil {
		t.Fatalf("initDB failed on S2 schema: %v", err)
	}

	// 3. Verify expires_at column exists in sandboxes table
	var hasExpiresAt bool
	infoRows, err := db.Query("PRAGMA table_info(sandboxes)")
	if err != nil {
		t.Fatalf("pragma table_info failed: %v", err)
	}
	for infoRows.Next() {
		var cid int
		var name, typeStr string
		var notNull int
		var dfltValue interface{}
		var pk int
		if err := infoRows.Scan(&cid, &name, &typeStr, &notNull, &dfltValue, &pk); err == nil {
			if name == "expires_at" {
				hasExpiresAt = true
			}
		}
	}
	infoRows.Close()

	if !hasExpiresAt {
		t.Fatalf("expires_at column missing after initDB on S2 schema")
	}

	// 4. Verify existing rows are unchanged with NULL expires_at
	var exp1, exp2 sql.NullInt64
	var st1, st2 string
	err = db.QueryRow("SELECT status, expires_at FROM sandboxes WHERE sandbox_id = 's2-sb-1'").Scan(&st1, &exp1)
	if err != nil || st1 != "ACTIVE" || exp1.Valid {
		t.Fatalf("s2-sb-1 mismatch: status=%s, exp.Valid=%v", st1, exp1.Valid)
	}
	err = db.QueryRow("SELECT status, expires_at FROM sandboxes WHERE sandbox_id = 's2-sb-2'").Scan(&st2, &exp2)
	if err != nil || st2 != "EXPIRING" || exp2.Valid {
		t.Fatalf("s2-sb-2 mismatch: status=%s, exp.Valid=%v", st2, exp2.Valid)
	}

	// 5. Verify allocateLease works on upgraded DB
	allocRes, err := allocateLease("s2-new-sb", "", "sandbox", "")
	if err != nil || allocRes == nil || allocRes.ExpiresAt == nil {
		t.Fatalf("allocateLease on upgraded S2 schema failed: %v", err)
	}

	// 6. Verify second initDB is a no-op
	if err := initDB(); err != nil {
		t.Fatalf("second initDB on upgraded S2 schema failed: %v", err)
	}
}

func TestExtendSandboxMaxInt64(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("getDBConnection failed: %v", err)
	}
	defer db.Close()

	sbID := "maxint-sb"
	initialExp := int64(1700000000)
	_, err = db.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at)
		VALUES (?, ?, 'sandbox', '10.20.30.77', 'ACTIVE', ?)
	`, sbID, sbID+".cstam.felcloud.tn", initialExp)
	if err != nil {
		t.Fatalf("insert test sandbox failed: %v", err)
	}

	// Test with math.MaxInt64
	_, err = extendSandbox(db, sbID, math.MaxInt64)
	if err == nil || (!errors.Is(err, ErrTTLCapExceeded) && !errors.Is(err, ErrInvalidExtend)) {
		t.Fatalf("expected ErrTTLCapExceeded or ErrInvalidExtend for math.MaxInt64, got %v", err)
	}

	// Test with math.MaxInt64 - 1
	_, err = extendSandbox(db, sbID, math.MaxInt64-1)
	if err == nil || (!errors.Is(err, ErrTTLCapExceeded) && !errors.Is(err, ErrInvalidExtend)) {
		t.Fatalf("expected ErrTTLCapExceeded or ErrInvalidExtend for math.MaxInt64-1, got %v", err)
	}

	// Assert expires_at unchanged in database
	var currentExp int64
	err = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&currentExp)
	if err != nil || currentExp != initialExp {
		t.Fatalf("expected expires_at unchanged (%d), got %d, err=%v", initialExp, currentExp, err)
	}
}

func TestParseTTLConfig(t *testing.T) {
	// 1. Defaults when environment is empty
	def, max, min, err := parseTTLConfig(func(k string) string { return "" })
	if err != nil || def != 14400 || max != 86400 || min != 60 {
		t.Fatalf("default parsing mismatch: def=%d, max=%d, min=%d, err=%v", def, max, min, err)
	}

	// 2. Custom valid values
	def, max, min, err = parseTTLConfig(func(k string) string {
		switch k {
		case "DEFAULT_TTL_SECONDS":
			return "3600"
		case "MAX_TTL_SECONDS":
			return "7200"
		case "MIN_EXTEND_SECONDS":
			return "120"
		default:
			return ""
		}
	})
	if err != nil || def != 3600 || max != 7200 || min != 120 {
		t.Fatalf("custom valid parsing mismatch: def=%d, max=%d, min=%d, err=%v", def, max, min, err)
	}

	// 3. Set-but-unparseable values
	_, _, _, err = parseTTLConfig(func(k string) string {
		if k == "DEFAULT_TTL_SECONDS" {
			return "not_a_number"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "invalid DEFAULT_TTL_SECONDS") {
		t.Fatalf("expected unparseable DEFAULT_TTL_SECONDS error, got %v", err)
	}

	_, _, _, err = parseTTLConfig(func(k string) string {
		if k == "MAX_TTL_SECONDS" {
			return "invalid_max"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "invalid MAX_TTL_SECONDS") {
		t.Fatalf("expected unparseable MAX_TTL_SECONDS error, got %v", err)
	}

	_, _, _, err = parseTTLConfig(func(k string) string {
		if k == "MIN_EXTEND_SECONDS" {
			return "invalid_min"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "invalid MIN_EXTEND_SECONDS") {
		t.Fatalf("expected unparseable MIN_EXTEND_SECONDS error, got %v", err)
	}

	// 4. Default > Max
	_, _, _, err = parseTTLConfig(func(k string) string {
		switch k {
		case "DEFAULT_TTL_SECONDS":
			return "50000"
		case "MAX_TTL_SECONDS":
			return "10000"
		default:
			return ""
		}
	})
	if err == nil || !strings.Contains(err.Error(), "invalid TTL configuration") {
		t.Fatalf("expected default > max error, got %v", err)
	}

	// 5. Negative values
	_, _, _, err = parseTTLConfig(func(k string) string {
		if k == "MIN_EXTEND_SECONDS" {
			return "-10"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("expected negative value error, got nil")
	}
}

func TestAllocationPreservesActiveExpiresAt(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	// Initial allocation at baseTime
	res1, err := allocateLeaseTTL("sb-preserve", "", "sandbox", "", 3600)
	if err != nil || res1.ExpiresAt == nil || *res1.ExpiresAt != baseTime+3600 {
		t.Fatalf("initial allocation failed: %v", err)
	}
	origExp := *res1.ExpiresAt

	// Advance time by 1000s
	currTime = baseTime + 1000

	// Re-allocate the same ACTIVE sandbox
	res2, err := allocateLeaseTTL("sb-preserve", "", "sandbox", "", 7200)
	if err != nil {
		t.Fatalf("re-allocation failed: %v", err)
	}
	if res2.ExpiresAt == nil || *res2.ExpiresAt != origExp {
		t.Fatalf("expected re-allocation to keep existing expires_at %d, got %v", origExp, res2.ExpiresAt)
	}

	// Check DB row directly
	db, _ := getDBConnection()
	defer db.Close()
	var dbExp int64
	_ = db.QueryRow("SELECT expires_at FROM sandboxes WHERE sandbox_id = 'sb-preserve'").Scan(&dbExp)
	if dbExp != origExp {
		t.Fatalf("expected DB expires_at %d, got %d", origExp, dbExp)
	}
}

func TestAllocateLeaseExistingSandboxStates(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	t.Run("EXPIRING", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-expiring', 'sb-expiring.local', 'sandbox', '10.0.0.99', 'EXPIRING', 1700000000)")
		if err != nil {
			t.Fatalf("insert expiring sandbox: %v", err)
		}
		var sbCountBefore, ipCountBefore int
		_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes").Scan(&sbCountBefore)
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations").Scan(&ipCountBefore)
		var rowBefore string
		_ = db.QueryRow("SELECT status || '|' || COALESCE(private_ip, 'NULL') || '|' || COALESCE(expires_at, -1) FROM sandboxes WHERE sandbox_id = 'sb-expiring'").Scan(&rowBefore)

		_, err = allocateLeaseTTL("sb-expiring", "", "sandbox", "", 3600)
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("expected ErrWrongState for EXPIRING sandbox, got %v", err)
		}

		var sbCountAfter, ipCountAfter int
		_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes").Scan(&sbCountAfter)
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations").Scan(&ipCountAfter)
		var rowAfter string
		_ = db.QueryRow("SELECT status || '|' || COALESCE(private_ip, 'NULL') || '|' || COALESCE(expires_at, -1) FROM sandboxes WHERE sandbox_id = 'sb-expiring'").Scan(&rowAfter)
		if sbCountBefore != sbCountAfter || ipCountBefore != ipCountAfter {
			t.Fatalf("expected zero writes: sb (%d->%d), ip (%d->%d)", sbCountBefore, sbCountAfter, ipCountBefore, ipCountAfter)
		}
		if rowBefore != rowAfter {
			t.Fatalf("expected sandbox row unchanged, before=%s after=%s", rowBefore, rowAfter)
		}
	})

	t.Run("TEARING_DOWN", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-teardown', 'sb-teardown.local', 'sandbox', '10.0.0.98', 'TEARING_DOWN', 1700000000)")
		if err != nil {
			t.Fatalf("insert tearing_down sandbox: %v", err)
		}
		var sbCountBefore, ipCountBefore int
		_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes").Scan(&sbCountBefore)
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations").Scan(&ipCountBefore)
		var rowBefore string
		_ = db.QueryRow("SELECT status || '|' || COALESCE(private_ip, 'NULL') || '|' || COALESCE(expires_at, -1) FROM sandboxes WHERE sandbox_id = 'sb-teardown'").Scan(&rowBefore)

		_, err = allocateLeaseTTL("sb-teardown", "", "sandbox", "", 3600)
		if !errors.Is(err, ErrWrongState) {
			t.Fatalf("expected ErrWrongState for TEARING_DOWN sandbox, got %v", err)
		}

		var sbCountAfter, ipCountAfter int
		_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes").Scan(&sbCountAfter)
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations").Scan(&ipCountAfter)
		var rowAfter string
		_ = db.QueryRow("SELECT status || '|' || COALESCE(private_ip, 'NULL') || '|' || COALESCE(expires_at, -1) FROM sandboxes WHERE sandbox_id = 'sb-teardown'").Scan(&rowAfter)
		if sbCountBefore != sbCountAfter || ipCountBefore != ipCountAfter {
			t.Fatalf("expected zero writes: sb (%d->%d), ip (%d->%d)", sbCountBefore, sbCountAfter, ipCountBefore, ipCountAfter)
		}
		if rowBefore != rowAfter {
			t.Fatalf("expected sandbox row unchanged, before=%s after=%s", rowBefore, rowAfter)
		}
	})

	t.Run("ACTIVE_with_IP", func(t *testing.T) {
		res1, err := allocateLeaseTTL("sb-active-ip", "", "sandbox", "", 3600)
		if err != nil {
			t.Fatalf("initial allocation failed: %v", err)
		}
		origIP := res1.IPAddress
		origExp := *res1.ExpiresAt

		var ipCountBefore int
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE entity_name = 'sb-active-ip'").Scan(&ipCountBefore)

		res2, err := allocateLeaseTTL("sb-active-ip", "", "sandbox", "", 7200)
		if err != nil {
			t.Fatalf("re-allocation failed: %v", err)
		}
		if res2.IPAddress != origIP {
			t.Fatalf("expected same IP %s, got %s", origIP, res2.IPAddress)
		}
		if *res2.ExpiresAt != origExp {
			t.Fatalf("expected same expires_at %d, got %d", origExp, *res2.ExpiresAt)
		}

		var ipCountAfter int
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE entity_name = 'sb-active-ip'").Scan(&ipCountAfter)
		if ipCountBefore != 1 || ipCountAfter != 1 {
			t.Fatalf("expected IP count to remain 1, got before=%d after=%d", ipCountBefore, ipCountAfter)
		}
	})

	t.Run("ACTIVE_with_NULL_IP", func(t *testing.T) {
		origExp := int64(1750000000)
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-active-null-ip', 'sb-active-null-ip.local', 'sandbox', NULL, 'ACTIVE', ?)", origExp)
		if err != nil {
			t.Fatalf("insert active null ip sandbox: %v", err)
		}

		res, err := allocateLeaseTTL("sb-active-null-ip", "", "sandbox", "", 3600)
		if err != nil {
			t.Fatalf("allocation for ACTIVE null ip sandbox failed: %v", err)
		}
		if res.IPAddress == "" {
			t.Fatalf("expected newly allocated IP, got empty")
		}
		if res.ExpiresAt == nil || *res.ExpiresAt != origExp {
			t.Fatalf("expected preserved expires_at %d, got %v", origExp, res.ExpiresAt)
		}
	})

	t.Run("RELEASED", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-released', 'sb-released.local', 'sandbox', NULL, 'RELEASED', NULL)")
		if err != nil {
			t.Fatalf("insert released sandbox: %v", err)
		}
		res, err := allocateLeaseTTL("sb-released", "", "sandbox", "", 3600)
		if err != nil || res.Status != "ALLOCATED" {
			t.Fatalf("expected successful allocation for RELEASED sandbox, got res: %v, err: %v", res, err)
		}
	})

	t.Run("FAILED", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-failed', 'sb-failed.local', 'sandbox', NULL, 'FAILED', NULL)")
		if err != nil {
			t.Fatalf("insert failed sandbox: %v", err)
		}
		res, err := allocateLeaseTTL("sb-failed", "", "sandbox", "", 3600)
		if err != nil || res.Status != "ALLOCATED" {
			t.Fatalf("expected successful allocation for FAILED sandbox, got res: %v, err: %v", res, err)
		}
	})

	t.Run("new", func(t *testing.T) {
		res, err := allocateLeaseTTL("sb-new-unique", "", "sandbox", "", 3600)
		if err != nil || res.Status != "ALLOCATED" {
			t.Fatalf("expected successful allocation for new sandbox, got res: %v, err: %v", res, err)
		}
	})
}

func TestReleaseLeaseTearingDownGuard(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-td-guard', 'sb-td-guard.local', 'sandbox', '10.0.0.95', 'TEARING_DOWN', 1700000000)")
	if err != nil {
		t.Fatalf("insert tearing_down sandbox: %v", err)
	}
	_, err = db.Exec("INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status) VALUES ('10.0.0.95', 'sandbox', 'sb-td-guard', 'TEAM_SANDBOX', 'ALLOCATED')")
	if err != nil {
		t.Fatalf("insert allocation: %v", err)
	}

	_, err = releaseLease("sb-td-guard", "", 60)
	if !errors.Is(err, ErrWrongState) {
		t.Fatalf("expected ErrWrongState from releaseLease on TEARING_DOWN sandbox, got %v", err)
	}

	var status, privateIP string
	if err := db.QueryRow("SELECT status, COALESCE(private_ip, '') FROM sandboxes WHERE sandbox_id = 'sb-td-guard'").Scan(&status, &privateIP); err != nil {
		t.Fatalf("failed to query sandbox: %v", err)
	}
	if status != "TEARING_DOWN" || privateIP != "10.0.0.95" {
		t.Fatalf("expected sandbox to remain TEARING_DOWN with IP 10.0.0.95, got status=%s, privateIP=%s", status, privateIP)
	}

	var ipStatus string
	if err := db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = '10.0.0.95'").Scan(&ipStatus); err != nil {
		t.Fatalf("failed to query ip_allocations: %v", err)
	}
	if ipStatus != "ALLOCATED" {
		t.Fatalf("expected IP row to remain ALLOCATED (no QUARANTINED write), got %s", ipStatus)
	}

	var releasedEvents int
	_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = 'sb-td-guard' AND type = 'released'").Scan(&releasedEvents)
	if releasedEvents != 0 {
		t.Fatalf("expected no 'released' event for rejected release, got %d", releasedEvents)
	}
}

func TestAddTeamFlowFailedHealthCheckGuard(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	// Sandbox owned by the reaper: must NOT be clobbered by a late health-check result.
	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-hc-td', 'sb-hc-td.local', 'sandbox', '10.0.0.94', 'TEARING_DOWN', 1700000000)")
	if err != nil {
		t.Fatalf("insert tearing_down sandbox: %v", err)
	}
	// Sandbox still in ACTIVE: the guarded write must remain effective.
	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-hc-active', 'sb-hc-active.local', 'sandbox', '10.0.0.93', 'ACTIVE', 1700000000)")
	if err != nil {
		t.Fatalf("insert active sandbox: %v", err)
	}

	markFailedHealthCheck("sb-hc-td")
	markFailedHealthCheck("sb-hc-active")

	var tdStatus string
	if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-hc-td'").Scan(&tdStatus); err != nil {
		t.Fatalf("failed to query TEARING_DOWN sandbox: %v", err)
	}
	if tdStatus != "TEARING_DOWN" {
		t.Fatalf("expected TEARING_DOWN sandbox unchanged by addTeamFlow health-check write, got %s", tdStatus)
	}

	var activeStatus string
	if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-hc-active'").Scan(&activeStatus); err != nil {
		t.Fatalf("failed to query ACTIVE sandbox: %v", err)
	}
	if activeStatus != "FAILED_HEALTH_CHECK" {
		t.Fatalf("expected ACTIVE sandbox to become FAILED_HEALTH_CHECK, got %s", activeStatus)
	}
}

func TestParseReaperConfig(t *testing.T) {
	// 1. Defaults
	intv, batch, maxAtt, ret, err := parseReaperConfig(func(string) string { return "" })
	if err != nil || intv != 10 || batch != 20 || maxAtt != 5 || ret != 259200 {
		t.Fatalf("expected defaults (10, 20, 5, 259200), got (%d, %d, %d, %d, err: %v)", intv, batch, maxAtt, ret, err)
	}

	// 2. Custom valid
	intv, batch, maxAtt, ret, err = parseReaperConfig(func(k string) string {
		switch k {
		case "REAPER_INTERVAL_SECONDS":
			return "15"
		case "REAPER_BATCH_SIZE":
			return "50"
		case "TEARDOWN_MAX_ATTEMPTS":
			return "3"
		case "EVENTS_RETENTION_SECONDS":
			return "86400"
		default:
			return ""
		}
	})
	if err != nil || intv != 15 || batch != 50 || maxAtt != 3 || ret != 86400 {
		t.Fatalf("expected custom values, got (%d, %d, %d, %d, err: %v)", intv, batch, maxAtt, ret, err)
	}

	// 3. Unparseable strings
	for _, envKey := range []string{"REAPER_INTERVAL_SECONDS", "REAPER_BATCH_SIZE", "TEARDOWN_MAX_ATTEMPTS", "EVENTS_RETENTION_SECONDS"} {
		_, _, _, _, err := parseReaperConfig(func(k string) string {
			if k == envKey {
				return "not_a_number"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), envKey) {
			t.Fatalf("expected error for unparseable %s, got %v", envKey, err)
		}
	}

	// 4. Non-positive values (< 1)
	for _, envKey := range []string{"REAPER_INTERVAL_SECONDS", "REAPER_BATCH_SIZE", "TEARDOWN_MAX_ATTEMPTS", "EVENTS_RETENTION_SECONDS"} {
		_, _, _, _, err := parseReaperConfig(func(k string) string {
			if k == envKey {
				return "0"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "invalid reaper configuration") {
			t.Fatalf("expected error for 0 value in %s, got %v", envKey, err)
		}
	}
}

func TestReaperExpiredActiveSandbox(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	// Allocate sandbox with 100s TTL
	res, err := allocateLeaseTTL("sb-reap-1", "", "sandbox", "", 100)
	if err != nil {
		t.Fatalf("allocation failed: %v", err)
	}
	allocatedIP := res.IPAddress
	if _, err := dbExecSandboxManagedBy(t, "sb-reap-1", "control-plane"); err != nil {
		t.Fatal(err)
	}

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	// Advance time past expiry
	currTime = baseTime + 150

	// Run reaper tick
	tickRes := reaperTick(db, nowUnix())
	if tickRes.Error != nil {
		t.Fatalf("reaperTick error: %v", tickRes.Error)
	}
	if tickRes.ReapedCount != 1 {
		t.Fatalf("expected 1 reaped sandbox, got %d", tickRes.ReapedCount)
	}

	// Assert sandbox is RELEASED
	var sbStatus string
	if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-reap-1'").Scan(&sbStatus); err != nil || sbStatus != "RELEASED" {
		t.Fatalf("expected sandbox status RELEASED, got '%s' (err: %v)", sbStatus, err)
	}

	// Assert IP is QUARANTINED
	var ipStatus string
	if err := db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = ?", allocatedIP).Scan(&ipStatus); err != nil || ipStatus != "QUARANTINED" {
		t.Fatalf("expected IP status QUARANTINED, got '%s' (err: %v)", ipStatus, err)
	}

	// Assert events recorded in order
	rows, err := db.Query("SELECT type, detail FROM events WHERE sandbox_id = 'sb-reap-1' ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()

	var eventTypes []string
	for rows.Next() {
		var typ, det string
		_ = rows.Scan(&typ, &det)
		eventTypes = append(eventTypes, typ)
	}
	rows.Close()

	// Events should include: allocated, teardown_step (release_ips), teardown_step (delete_openstack_resources), teardown_step (remove_route_and_sync), expired_teardown_done
	hasTeardownDone := false
	for _, typ := range eventTypes {
		if typ == "expired_teardown_done" {
			hasTeardownDone = true
		}
	}
	if !hasTeardownDone {
		t.Fatalf("expected expired_teardown_done event, got events: %v", eventTypes)
	}

	// Advance time past quarantine cooldown (default 60s)
	currTime += 70
	reaperTick(db, nowUnix())

	// Assert IP is now reclaimed to FREE
	if err := db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = ?", allocatedIP).Scan(&ipStatus); err != nil || ipStatus != "FREE" {
		t.Fatalf("expected IP status FREE after quarantine reclaim, got '%s' (err: %v)", ipStatus, err)
	}
}

func TestReaperUntouchedSandboxes(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	// Sandbox 1: not yet expired (TTL 3600)
	_, err := allocateLeaseTTL("sb-future", "", "sandbox", "", 3600)
	if err != nil {
		t.Fatalf("allocation future: %v", err)
	}

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	// Sandbox 2: NULL expires_at
	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-null-exp', 'sb-null.local', 'sandbox', '10.0.0.77', 'ACTIVE', NULL)")
	if err != nil {
		t.Fatalf("insert null expires_at sandbox: %v", err)
	}

	// Run reaper tick at baseTime
	res := reaperTick(db, nowUnix())
	if res.Error != nil || res.ReapedCount != 0 {
		t.Fatalf("expected 0 reaped, got count=%d, err=%v", res.ReapedCount, res.Error)
	}

	// Verify both sandboxes remain ACTIVE
	var status1, status2 string
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-future'").Scan(&status1)
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-null-exp'").Scan(&status2)
	if status1 != "ACTIVE" || status2 != "ACTIVE" {
		t.Fatalf("expected both sandboxes ACTIVE, got sb-future='%s', sb-null-exp='%s'", status1, status2)
	}
}

func TestReaperResumeAndIdempotency(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	oldSync := gatewaySyncFunc
	oldMaxAttempts := teardownMaxAttempts
	t.Cleanup(func() { gatewaySyncFunc, teardownMaxAttempts = oldSync, oldMaxAttempts })
	teardownMaxAttempts = 5
	syncCalls := 0
	gatewaySyncFunc = func(bool) (bool, string) {
		syncCalls++
		if syncCalls == 1 {
			return false, "transient failure"
		}
		return true, "ok"
	}

	_, err = allocateLeaseTTL("sb-flaky", "", "sandbox", "", 50)
	if err != nil {
		t.Fatalf("allocate failed: %v", err)
	}
	_, _ = dbExecSandboxManagedBy(t, "sb-flaky", "control-plane")

	// Advance past expiry
	currTime = baseTime + 100

	// Tick 1: flaky_step fails
	res1 := reaperTick(db, nowUnix())
	if res1.ReapedCount != 0 {
		t.Fatalf("expected 0 reaped on tick 1, got %d", res1.ReapedCount)
	}

	// Verify sandbox is in TEARING_DOWN
	var status string
	if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-flaky'").Scan(&status); err != nil || status != "TEARING_DOWN" {
		t.Fatalf("expected TEARING_DOWN on tick 1, got '%s'", status)
	}

	// A failed step must leave the lease allocated.
	var ipStatus string
	if err := db.QueryRow("SELECT status FROM ip_allocations WHERE entity_name = 'sb-flaky'").Scan(&ipStatus); err != nil || ipStatus != "ALLOCATED" {
		t.Fatalf("expected ALLOCATED on tick 1, got '%s' (err=%v)", ipStatus, err)
	}

	// Tick 2 resumes at the failed route step and completes the teardown.
	res2 := reaperTick(db, nowUnix())
	if res2.ResumedCount != 1 {
		t.Fatalf("expected 1 resumed sandbox on tick 2, got %d", res2.ResumedCount)
	}

	// Verify sandbox is now RELEASED
	if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-flaky'").Scan(&status); err != nil || status != "RELEASED" {
		t.Fatalf("expected RELEASED on tick 2, got '%s'", status)
	}
}

func TestReaperMaxAttempts(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	oldSync := gatewaySyncFunc
	oldMax := teardownMaxAttempts
	t.Cleanup(func() { gatewaySyncFunc, teardownMaxAttempts = oldSync, oldMax })
	teardownMaxAttempts = 3
	gatewaySyncFunc = func(bool) (bool, string) {
		return false, "permanent failure"
	}

	_, err = allocateLeaseTTL("sb-perm-fail", "", "sandbox", "", 50)
	if err != nil {
		t.Fatalf("allocate failed: %v", err)
	}
	_, _ = dbExecSandboxManagedBy(t, "sb-perm-fail", "control-plane")

	currTime = baseTime + 100

	// Tick 1
	reaperTick(db, nowUnix())
	var status string
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-perm-fail'").Scan(&status)
	if status != "TEARING_DOWN" {
		t.Fatalf("tick 1: expected TEARING_DOWN, got %s", status)
	}

	// Tick 2
	reaperTick(db, nowUnix())
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-perm-fail'").Scan(&status)
	if status != "TEARING_DOWN" {
		t.Fatalf("tick 2: expected TEARING_DOWN, got %s", status)
	}

	// Tick 3 reaches the durable max attempts -> TEARDOWN_FAILED.
	res3 := reaperTick(db, nowUnix())
	if res3.FailedCount != 1 {
		t.Fatalf("tick 3: expected 1 failure, got %d", res3.FailedCount)
	}
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-perm-fail'").Scan(&status)
	if status != "TEARDOWN_FAILED" {
		t.Fatalf("tick 3: expected TEARDOWN_FAILED, got %s", status)
	}

	// Verify teardown_failed event
	var failedEventCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = 'sb-perm-fail' AND type = 'TEARDOWN_FAILED'").Scan(&failedEventCount)
	if failedEventCount != 1 {
		t.Fatalf("expected 1 teardown_failed event, got %d", failedEventCount)
	}
}

func TestReaperStaleExpiringResumed(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	// Insert stale EXPIRING sandbox
	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at) VALUES ('sb-stale-exp', 'sb-stale.local', 'sandbox', '10.0.0.88', 'EXPIRING', ?)", baseTime-100)
	if err != nil {
		t.Fatalf("insert expiring sandbox: %v", err)
	}
	_, err = db.Exec("INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status) VALUES ('10.0.0.88', 'sandbox', 'sb-stale-exp', 'TEAM_SANDBOX', 'ALLOCATED')")
	if err != nil {
		t.Fatalf("insert allocation: %v", err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET managed_by='control-plane' WHERE sandbox_id='sb-stale-exp'`); err != nil {
		t.Fatal(err)
	}

	res := reaperTick(db, nowUnix())
	if res.ResumedCount != 1 {
		t.Fatalf("expected 1 resumed sandbox, got %d", res.ResumedCount)
	}

	var status string
	_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-stale-exp'").Scan(&status)
	if status != "RELEASED" {
		t.Fatalf("expected RELEASED, got %s", status)
	}
}

func TestReaperConcurrency(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	// Allocate 10 sandboxes with TTL 50
	for i := 1; i <= 10; i++ {
		sbID := fmt.Sprintf("sb-conc-%d", i)
		if _, err := allocateLeaseTTL(sbID, "", "sandbox", "", 50); err != nil {
			t.Fatalf("allocate %s: %v", sbID, err)
		}
		if _, err := db.Exec(`UPDATE sandboxes SET managed_by='control-plane' WHERE sandbox_id=?`, sbID); err != nil {
			t.Fatal(err)
		}
	}

	// Advance past expiry
	currTime = baseTime + 100

	// 2 goroutines calling reaperTick simultaneously
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reaperTickInner(db, nowUnix())
			if res.Error != nil {
				errCh <- res.Error
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("reaper concurrency error: %v", err)
	}

	// Assert all 10 sandboxes are RELEASED
	for i := 1; i <= 10; i++ {
		sbID := fmt.Sprintf("sb-conc-%d", i)
		var status string
		if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&status); err != nil || status != "RELEASED" {
			t.Fatalf("expected %s status RELEASED, got '%s'", sbID, status)
		}

		// Exactly one expired_teardown_done event each
		var doneCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = ? AND type = 'expired_teardown_done'", sbID).Scan(&doneCount)
		if doneCount != 1 {
			t.Fatalf("expected 1 expired_teardown_done for %s, got %d", sbID, doneCount)
		}
	}
}

func TestReaperConcurrencyThroughMutexWrapper(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	for i := 1; i <= 10; i++ {
		sbID := fmt.Sprintf("sb-wrap-%d", i)
		if _, err := allocateLeaseTTL(sbID, "", "sandbox", "", 50); err != nil {
			t.Fatalf("allocate %s: %v", sbID, err)
		}
		if _, err := db.Exec(`UPDATE sandboxes SET managed_by='control-plane' WHERE sandbox_id=?`, sbID); err != nil {
			t.Fatal(err)
		}
	}

	currTime = baseTime + 100

	// 2 goroutines calling the mutex-protected wrapper: ticks must serialize.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reaperTick(db, nowUnix())
			if res.Error != nil {
				errCh <- res.Error
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("reaper wrapper concurrency error: %v", err)
	}

	for i := 1; i <= 10; i++ {
		sbID := fmt.Sprintf("sb-wrap-%d", i)
		var status string
		if err := db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = ?", sbID).Scan(&status); err != nil || status != "RELEASED" {
			t.Fatalf("expected %s status RELEASED, got '%s' (err: %v)", sbID, status, err)
		}

		var doneCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = ? AND type = 'expired_teardown_done'", sbID).Scan(&doneCount)
		if doneCount != 1 {
			t.Fatalf("expected 1 expired_teardown_done for %s, got %d", sbID, doneCount)
		}
	}
}

func TestReaperBatchSize(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	oldBatch := reaperBatchSize
	t.Cleanup(func() { reaperBatchSize = oldBatch })
	reaperBatchSize = 20

	// Allocate 25 expired sandboxes directly
	for i := 1; i <= 25; i++ {
		sbID := fmt.Sprintf("sb-batch-%02d", i)
		ip := fmt.Sprintf("10.0.1.%d", i)
		_, err := db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at, managed_by) VALUES (?, ?, 'sandbox', ?, 'ACTIVE', ?, 'control-plane')", sbID, sbID+".local", ip, baseTime-10)
		if err != nil {
			t.Fatalf("insert %s: %v", sbID, err)
		}
		_, err = db.Exec("INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status) VALUES (?, 'sandbox', ?, 'TEAM_SANDBOX', 'ALLOCATED')", ip, sbID)
		if err != nil {
			t.Fatalf("insert ip %s: %v", ip, err)
		}
	}

	// Tick 1: should process exactly 20
	res1 := reaperTick(db, nowUnix())
	if res1.ReapedCount != 20 {
		t.Fatalf("tick 1: expected 20 reaped, got %d", res1.ReapedCount)
	}

	// 5 remain ACTIVE
	var activeCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM sandboxes WHERE status = 'ACTIVE'").Scan(&activeCount)
	if activeCount != 5 {
		t.Fatalf("tick 1: expected 5 active remaining, got %d", activeCount)
	}

	// Tick 2: processes remaining 5
	res2 := reaperTick(db, nowUnix())
	if res2.ReapedCount != 5 {
		t.Fatalf("tick 2: expected 5 reaped, got %d", res2.ReapedCount)
	}
}

func TestReaperReleaseIPsLeakedSecondIP(t *testing.T) {
	setupTestDB(t)

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	nowUnix = func() int64 { return currTime }
	t.Cleanup(func() { nowUnix = oldNow })

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	// Allocate sandbox
	res, err := allocateLeaseTTL("sb-leak-ip", "", "sandbox", "10.0.0.10", 50)
	if err != nil {
		t.Fatalf("allocation failed: %v", err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET managed_by='control-plane' WHERE sandbox_id='sb-leak-ip'`); err != nil {
		t.Fatal(err)
	}

	// Insert leaked second IP row pointing to same entity_name
	_, err = db.Exec("INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status) VALUES ('10.0.0.11', 'sandbox', 'sb-leak-ip', 'TEAM_SANDBOX', 'ALLOCATED')")
	if err != nil {
		t.Fatalf("insert leaked IP: %v", err)
	}

	// Advance past expiry
	currTime = baseTime + 100

	// Reaper tick
	reaperTick(db, nowUnix())

	// Both IPs must be QUARANTINED
	var status1, status2 string
	_ = db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = ?", res.IPAddress).Scan(&status1)
	_ = db.QueryRow("SELECT status FROM ip_allocations WHERE ip_address = '10.0.0.11'").Scan(&status2)
	if status1 != "QUARANTINED" || status2 != "QUARANTINED" {
		t.Fatalf("expected both IPs QUARANTINED, got %s=%s, 10.0.0.11=%s", res.IPAddress, status1, status2)
	}
}

func TestReaperPruneEvents(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	baseTime := int64(1700000000)
	currTime := baseTime
	oldNow := nowUnix
	oldRet := eventsRetentionSeconds
	t.Cleanup(func() {
		nowUnix = oldNow
		eventsRetentionSeconds = oldRet
	})
	nowUnix = func() int64 { return currTime }
	eventsRetentionSeconds = 100

	// Insert 1200 old events (older than baseTime - 100s)
	oldTsMs := (baseTime - 500) * 1000
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO events (ts_ms, sandbox_id, type, detail, duration_ms) VALUES (?, 'sb-prune', 'test_event', 'detail', 10)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 1200; i++ {
		if _, err := stmt.Exec(oldTsMs); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Chunk 1: deletes at most 500
	res1 := reaperTick(db, nowUnix())
	if res1.PrunedEvents != 500 {
		t.Fatalf("tick 1: expected 500 pruned, got %d", res1.PrunedEvents)
	}

	var remaining int
	_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = 'sb-prune'").Scan(&remaining)
	if remaining != 700 {
		t.Fatalf("tick 1: expected 700 remaining, got %d", remaining)
	}

	// Chunk 2: deletes 500
	res2 := reaperTick(db, nowUnix())
	if res2.PrunedEvents != 500 {
		t.Fatalf("tick 2: expected 500 pruned, got %d", res2.PrunedEvents)
	}

	// Chunk 3: deletes remaining 200
	res3 := reaperTick(db, nowUnix())
	if res3.PrunedEvents != 200 {
		t.Fatalf("tick 3: expected 200 pruned, got %d", res3.PrunedEvents)
	}

	_ = db.QueryRow("SELECT COUNT(*) FROM events WHERE sandbox_id = 'sb-prune'").Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("tick 3: expected 0 remaining, got %d", remaining)
	}
}

func TestStartReaperLifecycle(t *testing.T) {
	setupTestDB(t)

	db, err := getDBConnection()
	if err != nil {
		t.Fatalf("failed to get DB: %v", err)
	}
	defer db.Close()

	oldInt := reaperIntervalSeconds
	oldNow := nowUnix
	t.Cleanup(func() {
		reaperIntervalSeconds = oldInt
		nowUnix = oldNow
	})
	reaperIntervalSeconds = 1

	// Fake clock so the tick body does not depend on wall time
	baseTime := int64(1700000000)
	currTime := baseTime
	nowUnix = func() int64 { return currTime }

	ctx, cancel := context.WithCancel(context.Background())
	done, err := startReaper(ctx, db)
	if err != nil {
		t.Fatalf("startReaper failed: %v", err)
	}

	// Insert an expired sandbox to trigger the step
	_, err = db.Exec("INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at, managed_by) VALUES ('sb-panic', 'sb-panic.local', 'sandbox', '10.0.0.12', 'ACTIVE', 1000, 'control-plane')")
	if err != nil {
		t.Fatalf("insert sandbox: %v", err)
	}

	// The background tick should route the sandbox through the shared teardown path.
	deadline := time.Now().Add(5 * time.Second)
	sbStatus := ""
	for {
		_ = db.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = 'sb-panic'").Scan(&sbStatus)
		if sbStatus == "RELEASED" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sbStatus != "RELEASED" {
		t.Fatalf("expected sandbox RELEASED after >= 2 ticks (recovered panic), got %q", sbStatus)
	}
	if lastTickUnix.Load() != currTime {
		t.Fatalf("expected lastTickUnix gauge %d, got %d", currTime, lastTickUnix.Load())
	}

	// Cancel context to stop the reaper
	cancel()

	// Wait on done channel to verify graceful stop with no leftover goroutine
	select {
	case <-done:
		// Succeeded
	case <-time.After(3 * time.Second):
		t.Fatalf("reaper did not terminate cleanly on context cancellation")
	}

}
