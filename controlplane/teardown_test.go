package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func createTeardownFixture(t *testing.T, id string) (*MockProvisioner, string) {
	t.Helper()
	mock := &MockProvisioner{}
	setupTestDB(t)
	provisioner = mock
	provisionTimeout = 5 * time.Second
	teardownTimeout = 5 * time.Second
	teardownConcurrency = 4
	defaultQuarantineSeconds = 30
	gatewaySyncFunc = func(bool) (bool, string) { return true, "ok" }
	lease, err := allocateLeaseTTL(id, id+".cstam.felcloud.tn", "sandbox", "", 3600)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := mock.CreateSandbox(context.Background(), CreateReq{Team: id, FixedIP: lease.IPAddress})
	if err != nil {
		t.Fatal(err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET server_id=?,port_id=?,fixed_ip=?,managed_by='control-plane' WHERE sandbox_id=?`, resources.ServerID, resources.PortID, resources.FixedIP, id); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return mock, lease.IPAddress
}

func querySandboxState(t *testing.T, id string) string {
	t.Helper()
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var state string
	if err := db.QueryRow(`SELECT status FROM sandboxes WHERE sandbox_id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestTeardownSuccessAndIdempotency(t *testing.T) {
	mock, ip := createTeardownFixture(t, "tear-success")
	result, err := teardownSandbox(context.Background(), "tear-success")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "RELEASED" || result.TimingsMS["remove_route"] < 0 {
		t.Fatalf("unexpected teardown result: %+v", result)
	}
	state := querySandboxState(t, "tear-success")
	if state != "RELEASED" {
		t.Fatalf("sandbox status=%s", state)
	}
	db, _ := getDBConnection()
	defer db.Close()
	var leaseState string
	if err := db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name=? AND ip_address=?`, "tear-success", ip).Scan(&leaseState); err != nil || leaseState != "QUARANTINED" {
		t.Fatalf("lease status=%q err=%v", leaseState, err)
	}
	for _, event := range []string{"TEARDOWN_STARTED", "ROUTE_REMOVED", "SERVER_DELETED", "PORT_DELETED", "VERIFIED_GONE", "RELEASED"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE sandbox_id=? AND type=? AND duration_ms IS NOT NULL`, "tear-success", event).Scan(&count); err != nil || count != 1 {
			t.Errorf("event %s count=%d err=%v", event, count, err)
		}
	}
	mock.mu.Lock()
	deleteCalls := mock.Calls["delete_server"] + mock.Calls["delete_port"]
	mock.mu.Unlock()
	if _, err := teardownSandbox(context.Background(), "tear-success"); err != nil {
		t.Fatal(err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.Calls["delete_server"]+mock.Calls["delete_port"] != deleteCalls {
		t.Fatal("idempotent teardown repeated resource deletion")
	}
}

func TestNonControlPlaneSandboxIsRefusedEverywhereOnlyOnce(t *testing.T) {
	setupTestDB(t)
	provisioner = &MockProvisioner{Servers: map[string]bool{}}
	if _, err := allocateLeaseTTL("team1", "team1.cstam.felcloud.tn", "sandbox", "10.20.30.70", 3600); err != nil {
		t.Fatal(err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET expires_at=? WHERE sandbox_id='team1'`, nowUnix()-1); err != nil {
		db.Close()
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	setupAPIRoutes().ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/sandboxes/team1", nil))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"step":"guard"`) {
		db.Close()
		t.Fatalf("endpoint status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := teardownCLI("team1"); !errors.Is(err, errTeardownRefused) {
		db.Close()
		t.Fatalf("CLI teardown error=%v, want ownership refusal", err)
	}
	for i := 0; i < 2; i++ {
		if got := reaperTick(db, nowUnix()); got.Error != nil {
			db.Close()
			t.Fatalf("reaper tick %d: %v", i, got.Error)
		}
	}
	var status, managedBy, leaseStatus string
	var attempts, refused int
	if err := db.QueryRow(`SELECT status,managed_by,teardown_attempts FROM sandboxes WHERE sandbox_id='team1'`).Scan(&status, &managedBy, &attempts); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name='team1'`).Scan(&leaseStatus); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE sandbox_id='team1' AND type='TEARDOWN_REFUSED'`).Scan(&refused); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if status != "ACTIVE" || managedBy != "external" || leaseStatus != "ALLOCATED" || attempts != 0 || refused != 1 {
		t.Fatalf("state=%s managed_by=%s lease=%s attempts=%d refused_events=%d", status, managedBy, leaseStatus, attempts, refused)
	}
}

func TestReaperRefusalEventIsEmittedOnce(t *testing.T) {
	setupTestDB(t)
	if _, err := allocateLeaseTTL("legacy-expired", "legacy-expired.cstam.felcloud.tn", "sandbox", "10.20.30.72", 3600); err != nil {
		t.Fatal(err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sandboxes SET expires_at=? WHERE sandbox_id='legacy-expired'`, nowUnix()-1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if result := reaperTick(db, nowUnix()); result.Error != nil {
			t.Fatalf("tick %d: %v", i, result.Error)
		}
	}
	var count, attempts int
	var status string
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE sandbox_id='legacy-expired' AND type='TEARDOWN_REFUSED'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status,teardown_attempts FROM sandboxes WHERE sandbox_id='legacy-expired'`).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || status != "ACTIVE" || attempts != 0 {
		t.Fatalf("refusal events=%d status=%s attempts=%d", count, status, attempts)
	}
}

func TestTeardownFailureResumesAtFailedStep(t *testing.T) {
	tests := []struct {
		name string
		step string
	}{
		{name: "route", step: "remove_route"},
		{name: "server deletion", step: "delete_server"},
		{name: "port deletion", step: "delete_port"},
		{name: "verification", step: "verify_gone"},
		{name: "lease release", step: "release_lease"},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("tear-resume-%d", i)
			mock, ip := createTeardownFixture(t, id)
			mock.Failures = map[string]map[int]error{tc.step: {1: errors.New("injected " + tc.step + " failure")}}
			if _, err := teardownSandbox(context.Background(), id); err == nil {
				t.Fatal("expected first attempt to fail")
			} else {
				var stepErr *teardownStepError
				if !errors.As(err, &stepErr) {
					t.Fatalf("error=%v is not a step error", err)
				}
				wantStep := tc.step
				if tc.step == "delete_server" || tc.step == "delete_port" {
					wantStep = tc.step
				}
				if stepErr.Step != wantStep {
					t.Fatalf("failed step=%s want %s", stepErr.Step, wantStep)
				}
			}
			db, _ := getDBConnection()
			var leaseState, status string
			_ = db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name=? AND ip_address=?`, id, ip).Scan(&leaseState)
			_ = db.QueryRow(`SELECT status FROM sandboxes WHERE sandbox_id=?`, id).Scan(&status)
			db.Close()
			if status != "TEARING_DOWN" || leaseState != "ALLOCATED" {
				t.Fatalf("after failure status=%s lease=%s; must not release before completion", status, leaseState)
			}
			if _, err := teardownSandbox(context.Background(), id); err != nil {
				t.Fatalf("retry did not resume: %v", err)
			}
			status = querySandboxState(t, id)
			if status != "RELEASED" {
				t.Fatalf("retry status=%s", status)
			}
		})
	}
}

func TestTeardownCrashResumeAndLegacyWithoutResources(t *testing.T) {
	mock, _ := createTeardownFixture(t, "tear-crash")
	mock.Failures = map[string]map[int]error{"remove_route": {1: errors.New("sync interrupted")}}
	if _, err := teardownSandbox(context.Background(), "tear-crash"); err == nil {
		t.Fatal("expected simulated interruption")
	}
	db, _ := getDBConnection()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := initDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := teardownSandbox(context.Background(), "tear-crash"); err != nil {
		t.Fatalf("resume after DB reopen: %v", err)
	}

	setupTestDB(t)
	mock = provisioner.(*MockProvisioner)
	_, err := allocateLeaseTTL("legacy-row", "legacy-row.cstam.felcloud.tn", "sandbox", "", 3600)
	if err != nil {
		t.Fatal(err)
	}
	db, _ = getDBConnection()
	_, err = db.Exec(`UPDATE sandboxes SET server_id=NULL,port_id=NULL WHERE sandbox_id='legacy-row'`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := teardownSandbox(context.Background(), "legacy-row"); !errors.Is(err, errTeardownRefused) {
		t.Fatalf("legacy teardown error=%v, want ownership refusal", err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.Calls["delete_server"] != 0 || mock.Calls["delete_port"] != 0 {
		t.Fatal("legacy sandbox without recorded IDs triggered deletion")
	}
}

func TestTeardownEndpointConcurrentRequestGetsAccepted(t *testing.T) {
	createTeardownFixture(t, "tear-concurrent")
	started := make(chan struct{})
	release := make(chan struct{})
	gatewaySyncFunc = func(bool) (bool, string) {
		close(started)
		<-release
		return true, "ok"
	}
	handler := setupAPIRoutes()
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/sandboxes/tear-concurrent", nil)
		handler.ServeHTTP(first, req)
		close(done)
	}()
	<-started
	second := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/tear-concurrent/teardown", strings.NewReader(`{}`))
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusAccepted {
		t.Fatalf("concurrent request status=%d body=%s", second.Code, second.Body.String())
	}
	close(release)
	<-done
	if first.Code != http.StatusOK {
		t.Fatalf("first request status=%d body=%s", first.Code, first.Body.String())
	}
}

func TestTeardownEndpointValidationAndUnknown(t *testing.T) {
	setupTestDB(t)
	handler := setupAPIRoutes()
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodDelete, "/api/sandboxes/Bad_Name", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status=%d body=%s", bad.Code, bad.Body.String())
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodDelete, "/api/sandboxes/no-such-sandbox", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown ID status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestParseTeardownConfig(t *testing.T) {
	get := map[string]string{}
	lookup := func(key string) string { return get[key] }
	timeout, parallel, err := parseTeardownConfig(lookup)
	if err != nil || timeout != 120*time.Second || parallel != 4 {
		t.Fatalf("defaults timeout=%s concurrency=%d err=%v", timeout, parallel, err)
	}
	get["TEARDOWN_TIMEOUT_SECONDS"] = "45"
	get["TEARDOWN_CONCURRENCY"] = "12"
	timeout, parallel, err = parseTeardownConfig(lookup)
	if err != nil || timeout != 45*time.Second || parallel != 12 {
		t.Fatalf("configured timeout=%s concurrency=%d err=%v", timeout, parallel, err)
	}
	get["TEARDOWN_CONCURRENCY"] = "0"
	if _, _, err := parseTeardownConfig(lookup); err == nil {
		t.Fatal("expected invalid teardown concurrency to fail")
	}
}

func TestTwelveParallelTeardowns(t *testing.T) {
	setupTestDB(t)
	teardownConcurrency = 12
	var syncMu sync.Mutex
	syncCalls := 0
	gatewaySyncFunc = func(bool) (bool, string) {
		syncMu.Lock()
		syncCalls++
		syncMu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return true, "ok"
	}
	const n = 12
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("tear-parallel-%02d", i)
		if _, err := allocateLeaseTTL(id, id+".cstam.felcloud.tn", "sandbox", "", 3600); err != nil {
			t.Fatal(err)
		}
		if _, err := dbExecSandboxManagedBy(t, id, "control-plane"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("tear-parallel-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := teardownSandbox(context.Background(), id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("parallel teardown error: %v", err)
	}
	db, _ := getDBConnection()
	defer db.Close()
	var released, allocated int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sandboxes WHERE status='RELEASED' AND sandbox_id LIKE 'tear-parallel-%'`).Scan(&released)
	_ = db.QueryRow(`SELECT COUNT(*) FROM ip_allocations WHERE entity_name LIKE 'tear-parallel-%' AND status='QUARANTINED'`).Scan(&allocated)
	if released != n || allocated != n {
		t.Fatalf("released sandboxes=%d quarantined leases=%d; want %d each", released, allocated, n)
	}
	if allocated == 0 {
		t.Fatal("no unique leases quarantined")
	}
}

func TestReaperAndEndpointShareTeardownPath(t *testing.T) {
	setupTestDB(t)
	base := nowUnix()
	_, err := allocateLeaseTTL("shared-path", "shared-path.cstam.felcloud.tn", "sandbox", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := getDBConnection()
	defer db.Close()
	if _, err := db.Exec(`UPDATE sandboxes SET expires_at=?,managed_by='control-plane' WHERE sandbox_id='shared-path'`, base-1); err != nil {
		t.Fatal(err)
	}
	if got := reaperTick(db, base); got.ReapedCount != 1 {
		t.Fatalf("reaper result=%+v", got)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/sandboxes/shared-path", nil)
	setupAPIRoutes().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("endpoint status=%d body=%s", w.Code, w.Body.String())
	}
}
