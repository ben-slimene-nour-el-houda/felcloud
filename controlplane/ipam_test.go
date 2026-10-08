package main

import (
	"fmt"
	"path/filepath"
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
