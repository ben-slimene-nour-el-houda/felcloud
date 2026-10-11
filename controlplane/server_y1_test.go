package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestGatewayStoreUsesIPAMDatabase(t *testing.T) {
	setupTestDB(t)
	t.Setenv("IPAM_DB_PATH", dbPath)
	initEnv()

	workingDir := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	store, err := newGatewayStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()

	if _, err := allocateLease("fix-y1-db", "", "sandbox", ""); err != nil {
		t.Fatalf("allocate IPAM lease: %v", err)
	}
	t.Setenv("FAILOVER_THRESHOLD_MS", "invalid")
	if _, status := performGatewayFailover(store); status != http.StatusBadRequest {
		t.Fatalf("failover status=%d, want 400 for invalid threshold", status)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'failover'`).Scan(&count); err != nil {
		t.Fatalf("gateway store does not see IPAM events table: %v", err)
	}
	if count != 1 {
		t.Fatalf("gateway store sees %d failover events, want 1", count)
	}

	ipamDB, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer ipamDB.Close()
	var allocationCount, failoverCount int
	if err := ipamDB.QueryRow(`SELECT COUNT(*) FROM ip_allocations WHERE entity_name = 'fix-y1-db'`).Scan(&allocationCount); err != nil {
		t.Fatal(err)
	}
	if err := ipamDB.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'failover'`).Scan(&failoverCount); err != nil {
		t.Fatal(err)
	}
	if allocationCount != 1 || failoverCount != 1 {
		t.Fatalf("IPAM database sees allocation=%d failover events=%d, want 1 each", allocationCount, failoverCount)
	}
}

func TestParseIPQuarantineSeconds(t *testing.T) {
	tests := []struct {
		value string
		want  int
		bad   bool
	}{
		{value: "", want: 60},
		{value: "1", want: 1},
		{value: "86400", want: 86400},
		{value: "0", bad: true},
		{value: "-1", bad: true},
		{value: "86401", bad: true},
		{value: "invalid", bad: true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := parseIPQuarantineSeconds(test.value)
			if test.bad {
				if err == nil {
					t.Fatalf("parseIPQuarantineSeconds(%q) accepted invalid value", test.value)
				}
				if !strings.Contains(err.Error(), "IP_QUARANTINE_SECONDS") {
					t.Fatalf("error does not name the invalid setting: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}

func prepareY1Server(t *testing.T, ttlSeconds int, quarantineSeconds int) {
	t.Helper()
	dbFile := setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", testAdminToken)
	t.Setenv("IPAM_DB_PATH", dbFile)
	t.Setenv("PROVISIONER", "mock")
	t.Setenv("LISTEN_ADDR", "127.0.0.1")
	t.Setenv("DEFAULT_TTL_SECONDS", strconv.Itoa(ttlSeconds))
	t.Setenv("REAPER_INTERVAL_SECONDS", "1")
	t.Setenv("IP_QUARANTINE_SECONDS", strconv.Itoa(quarantineSeconds))
	t.Setenv("RATE_LIMIT_PER_MIN", "1000")
	initEnv()
}

func TestRunServerRejectsInvalidSecurityAtStartup(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", "too-short")
	err := runServer(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "API security configuration") {
		t.Fatalf("runServer error=%v, want explicit API security configuration failure", err)
	}
}

func startY1Server(t *testing.T) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, port)
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("server exited before becoming healthy: %v", err)
		default:
		}
		response, err := http.Get(baseURL + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return baseURL, cancel, done
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after startup timeout")
	}
	t.Fatal("server did not become healthy")
	return "", nil, nil
}

func stopY1Server(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5 seconds")
	}
}

func TestServeConfiguresMockProvisioner(t *testing.T) {
	prepareY1Server(t, 60, 2)
	baseURL, cancel, done := startY1Server(t)
	defer stopY1Server(t, cancel, done)

	body := bytes.NewBufferString(`{"team":"fix-y1-demo"}`)
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/sandboxes/provision", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("provision status=%d, want 201", response.StatusCode)
	}
}

func TestReaperRunsInServer(t *testing.T) {
	prepareY1Server(t, 2, 2)
	baseURL, cancel, done := startY1Server(t)
	defer stopY1Server(t, cancel, done)

	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/sandboxes/provision", bytes.NewBufferString(`{"team":"fix-y1-reaper"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("provision status=%d, want 201", response.StatusCode)
	}

	deadline := time.Now().Add(12 * time.Second)
	sawQuarantined := false
	var quarantinedAt time.Time
	for time.Now().Before(deadline) {
		db, err := getDBConnection()
		if err != nil {
			t.Fatal(err)
		}
		var sandboxStatus, leaseStatus string
		err = db.QueryRow(`SELECT status FROM sandboxes WHERE sandbox_id = 'fix-y1-reaper'`).Scan(&sandboxStatus)
		if err == nil {
			err = db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name = 'fix-y1-reaper'`).Scan(&leaseStatus)
		}
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
		if leaseStatus == "QUARANTINED" {
			if !sawQuarantined {
				sawQuarantined = true
				quarantinedAt = time.Now()
			}
		}
		if sandboxStatus == "RELEASED" && leaseStatus == "FREE" {
			if !sawQuarantined {
				t.Fatal("lease became FREE without observing QUARANTINED")
			}
			if elapsed := time.Since(quarantinedAt); elapsed > 4*time.Second {
				t.Fatalf("lease reclaim took %s after quarantine, want at most 4 seconds", elapsed)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server reaper did not release the sandbox and reclaim its lease within 12 seconds")
}

func TestServerHealthIncludesReaperMetrics(t *testing.T) {
	prepareY1Server(t, 60, 2)
	baseURL, cancel, done := startY1Server(t)
	defer stopY1Server(t, cancel, done)

	response, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reaper_last_tick_unix", "reaper_last_tick_duration_ms", "oldest_overdue_seconds"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("health response missing %q: %#v", key, payload)
		}
	}
}
