package main

import (
	"bytes"
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

	if err := initDB(); err != nil {
		t.Fatalf("failed to init test DB: %v", err)
	}
	return dbFile
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



