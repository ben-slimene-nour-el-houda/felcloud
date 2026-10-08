package main

import (
	"fmt"
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

