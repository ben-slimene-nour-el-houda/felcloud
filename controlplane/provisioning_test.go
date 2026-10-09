package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func provisionTestSetup(t *testing.T, mock *MockProvisioner) {
	t.Helper()
	setupTestDB(t)
	provisioner = mock
	provisionTimeout = 2 * time.Second
	provisionSemaphore = make(chan struct{}, 8)
	routingDomain = "cstam.felcloud.tn"
	defaultQuarantineSeconds = 2
	provisionSync = provisionSyncState{}
	provisionNameLocks = sync.Map{}
	provisionHealthProbe = func(context.Context, string) (bool, string) { return true, "ok" }
	healthProbeTimeout = 90 * time.Second
	healthProbeRetryInterval = 500 * time.Millisecond
	healthProbeMode = "direct"
	healthProbeSSH = healthProbeSSHConfig{}
	healthProbeRunner = runHealthProbeAttempt
	gatewaySyncFunc = func(bool) (bool, string) { return true, "ok" }
}

func callProvision(team string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(CreateReq{Team: team})
	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/provision", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleProvision(rec, req)
	return rec
}

func leaseStatus(t *testing.T, team string) (string, string) {
	t.Helper()
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, ip string
	err = db.QueryRow(`SELECT status,private_ip FROM sandboxes WHERE sandbox_id=?`, team).Scan(&status, &ip)
	if err != nil {
		t.Fatal(err)
	}
	return status, ip
}

func TestProvisionFlowAndCompensation(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*testing.T, *MockProvisioner)
		wantStatus int
		wantState  string
		wantStep   string
	}{
		{name: "success", wantStatus: http.StatusCreated, wantState: "ACTIVE"},
		{name: "port creation failure", configure: func(_ *testing.T, m *MockProvisioner) { m.CreateErr = errors.New("port rejected") }, wantStatus: http.StatusBadGateway, wantState: "FAILED", wantStep: "openstack_create"},
		{name: "server ERROR", configure: func(_ *testing.T, m *MockProvisioner) { m.Statuses = []string{"ERROR"} }, wantStatus: http.StatusBadGateway, wantState: "FAILED", wantStep: "server_error"},
		{name: "timeout", configure: func(_ *testing.T, m *MockProvisioner) {
			m.Statuses = []string{"BUILD"}
			provisionTimeout = 15 * time.Millisecond
		}, wantStatus: http.StatusGatewayTimeout, wantState: "FAILED", wantStep: "server_timeout"},
		{name: "health failure", configure: func(_ *testing.T, _ *MockProvisioner) {
			provisionHealthProbe = func(context.Context, string) (bool, string) { return false, "unreachable" }
		}, wantStatus: http.StatusBadGateway, wantState: "FAILED", wantStep: "health"},
		{name: "sync failure", configure: func(_ *testing.T, _ *MockProvisioner) {
			gatewaySyncFunc = func(bool) (bool, string) { return false, "ansible failed" }
		}, wantStatus: http.StatusBadGateway, wantState: "FAILED", wantStep: "gateway_sync"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &MockProvisioner{}
			provisionTestSetup(t, mock)
			if tc.configure != nil {
				tc.configure(t, mock)
			}
			rec := callProvision("team-test")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.wantState != "" {
				state, ip := leaseStatus(t, "team-test")
				if state != tc.wantState {
					t.Fatalf("state=%s want %s", state, tc.wantState)
				}
				if state == "FAILED" {
					db, _ := getDBConnection()
					defer db.Close()
					var lease string
					err := db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name='team-test' AND ip_address=?`, ip).Scan(&lease)
					if err != nil || lease != "QUARANTINED" {
						t.Fatalf("lease state=%q err=%v", lease, err)
					}
					var releasedDetail, failedDetail string
					if err := db.QueryRow(`SELECT detail FROM events WHERE sandbox_id='team-test' AND type='released' ORDER BY id DESC LIMIT 1`).Scan(&releasedDetail); err != nil {
						t.Fatalf("missing released event: %v", err)
					}
					if !strings.Contains(releasedDetail, "ip="+ip+",cooldown=") {
						t.Errorf("released detail=%q", releasedDetail)
					}
					if err := db.QueryRow(`SELECT detail FROM events WHERE sandbox_id='team-test' AND type='FAILED' ORDER BY id DESC LIMIT 1`).Scan(&failedDetail); err != nil {
						t.Fatalf("missing FAILED event: %v", err)
					}
					if !strings.Contains(failedDetail, "step="+tc.wantStep+" error=") {
						t.Errorf("FAILED detail=%q", failedDetail)
					}
				}
			}
			if tc.wantStep != "" {
				var body map[string]interface{}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body["step"] != tc.wantStep {
					t.Fatalf("step=%q body=%v", body["step"], body)
				}
				if tc.name == "health failure" && (body["health_probe_mode"] != "direct" || body["last_probe_result"] != "unreachable") {
					t.Errorf("health probe diagnostics missing from error: %v", body)
				}
			}
			if tc.name != "success" {
				mock.mu.Lock()
				deleted := len(mock.Deleted)
				mock.mu.Unlock()
				if deleted < 2 {
					t.Fatalf("compensation did not invoke server/port deletion: %v", mock.Deleted)
				}
			}
			if tc.name == "success" {
				db, err := getDBConnection()
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for eventType, wantDuration := range map[string]int64{"PORT_CREATED": 11, "SERVER_CREATED": 22, "HEALTH_OK": -1, "ACTIVE": -1, "SYNC": -1} {
					var duration int64
					if err := db.QueryRow(`SELECT duration_ms FROM events WHERE sandbox_id='team-test' AND type=? ORDER BY id DESC LIMIT 1`, eventType).Scan(&duration); err != nil {
						t.Errorf("missing %s event: %v", eventType, err)
					} else if wantDuration >= 0 && duration != wantDuration {
						t.Errorf("%s duration=%d want %d", eventType, duration, wantDuration)
					} else if wantDuration < 0 && duration < 0 {
						t.Errorf("%s duration=%d, want non-negative", eventType, duration)
					}
				}
			}
		})
	}
}

func TestProvisionIdempotencyAndNameValidation(t *testing.T) {
	mock := &MockProvisioner{}
	provisionTestSetup(t, mock)
	first := callProvision("team-repeat")
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	second := callProvision("team-repeat")
	if second.Code != http.StatusOK {
		t.Fatalf("second: %d %s", second.Code, second.Body.String())
	}
	if mock.Created != 1 {
		t.Fatalf("created %d resources, want 1", mock.Created)
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/sandboxes/provision", bytes.NewBufferString(`{"team":"Upper"}`))
	rec := httptest.NewRecorder()
	handleProvision(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid team status=%d body=%s", rec.Code, rec.Body.String())
	}
	unknown := httptest.NewRequest(http.MethodPost, "/api/sandboxes/provision", bytes.NewBufferString(`{"team":"good","unexpected":true}`))
	rec = httptest.NewRecorder()
	handleProvision(rec, unknown)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", rec.Code)
	}
}

func TestConfigureHealthProbeTimeout(t *testing.T) {
	t.Setenv("HEALTH_PROBE_TIMEOUT_SECONDS", "")
	if err := configureHealthProbeTimeout(); err != nil {
		t.Fatal(err)
	}
	if healthProbeTimeout != 90*time.Second {
		t.Fatalf("default health probe timeout=%s, want 90s", healthProbeTimeout)
	}
	t.Setenv("HEALTH_PROBE_TIMEOUT_SECONDS", "45")
	if err := configureHealthProbeTimeout(); err != nil {
		t.Fatal(err)
	}
	if healthProbeTimeout != 45*time.Second {
		t.Fatalf("configured health probe timeout=%s, want 45s", healthProbeTimeout)
	}
	t.Setenv("HEALTH_PROBE_TIMEOUT_SECONDS", "0")
	if err := configureHealthProbeTimeout(); err == nil {
		t.Fatal("expected invalid health probe timeout to fail")
	}
}

func TestConfigureHealthProbeMode(t *testing.T) {
	t.Setenv("HEALTH_PROBE_MODE", "")
	if err := configureHealthProbe(); err != nil {
		t.Fatal(err)
	}
	if healthProbeMode != "direct" {
		t.Fatalf("default mode=%q, want direct", healthProbeMode)
	}
	t.Setenv("HEALTH_PROBE_MODE", "unsupported")
	if err := configureHealthProbe(); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
}

func TestLoadHealthProbeSSHConfigFromInventory(t *testing.T) {
	t.Setenv("ANSIBLE_INVENTORY", filepath.Join("..", "ansible", "inventory", "hosts.yml"))
	t.Setenv("HEALTH_PROBE_SSH_HOST", "")
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "")
	t.Setenv("HEALTH_PROBE_SSH_KEY", "")
	t.Setenv("BASTION_IP", "198.51.100.10")
	cfg, err := loadHealthProbeSSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "10.20.10.24" || cfg.User != "ansible" || cfg.Jump != "ansible@198.51.100.10" {
		t.Fatalf("inventory SSH settings = %+v", cfg)
	}
	if !strings.HasSuffix(filepath.ToSlash(cfg.Key), "ansible/.ssh/id_rsa_felcloud") {
		t.Fatalf("inventory SSH key path=%q", cfg.Key)
	}
}

func TestHealthProbeRetryModes(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		attempt    func(int) (string, error)
		wantOK     bool
		wantResult string
	}{
		{name: "success after retries", mode: "direct", attempt: func(n int) (string, error) {
			if n < 3 {
				return "000", nil
			}
			return "200", nil
		}, wantOK: true, wantResult: "HTTP 200"},
		{name: "never succeeds", mode: "direct", attempt: func(int) (string, error) { return "503", nil }, wantResult: "HTTP 503"},
		{name: "SSH error", mode: "gateway-ssh", attempt: func(int) (string, error) { return "", errors.New("SSH error: connection refused") }, wantResult: "SSH error: connection refused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provisionTestSetup(t, &MockProvisioner{})
			healthProbeTimeout = 12 * time.Millisecond
			healthProbeRetryInterval = time.Millisecond
			if tc.wantOK {
				healthProbeTimeout = 3 * time.Second
			}
			healthProbeMode = tc.mode
			attempts := 0
			healthProbeRunner = func(_ context.Context, mode, ip string) (string, error) {
				if mode != tc.mode || ip != "10.20.30.40" {
					t.Fatalf("runner got mode=%q ip=%q", mode, ip)
				}
				attempts++
				return tc.attempt(attempts)
			}
			ok, result := runHealthProbe(context.Background(), "10.20.30.40")
			if ok != tc.wantOK || result != tc.wantResult {
				t.Fatalf("probe=(%v,%q), want (%v,%q)", ok, result, tc.wantOK, tc.wantResult)
			}
			if tc.name == "success after retries" && attempts < 3 {
				t.Fatalf("attempts=%d, expected success after at least 3 attempts", attempts)
			}
			if tc.name != "success after retries" && attempts < 2 {
				t.Fatalf("attempts=%d, expected retry attempts", attempts)
			}
		})
	}
}

func TestHealthProbeRejectsNonIPv4(t *testing.T) {
	called := false
	healthProbeRunner = func(context.Context, string, string) (string, error) {
		called = true
		return "200", nil
	}
	if ok, result := runHealthProbe(context.Background(), "example.com"); ok || !strings.Contains(result, "literal IPv4") {
		t.Fatalf("probe=(%v,%q), expected literal IPv4 rejection", ok, result)
	}
	if called {
		t.Fatal("probe runner called for invalid IP")
	}
}

func TestConcurrentProvisioningAndSyncCoalescing(t *testing.T) {
	mock := &MockProvisioner{}
	provisionTestSetup(t, mock)
	provisionTimeout = 5 * time.Second
	var syncMu sync.Mutex
	syncCalls := 0
	gatewaySyncFunc = func(bool) (bool, string) {
		syncMu.Lock()
		syncCalls++
		syncMu.Unlock()
		time.Sleep(120 * time.Millisecond)
		return true, "ok"
	}
	const n = 12
	var wg sync.WaitGroup
	codes := make(chan int, n)
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := callProvision(fmt.Sprintf("parallel-%02d", i))
			codes <- rec.Code
			if rec.Code != http.StatusCreated {
				errs <- rec.Body.String()
			}
		}(i)
	}
	wg.Wait()
	close(codes)
	close(errs)
	for msg := range errs {
		t.Errorf("provision error: %s", msg)
	}
	tips := map[string]bool{}
	for i := 0; i < n; i++ {
		team := fmt.Sprintf("parallel-%02d", i)
		state, ip := leaseStatus(t, team)
		if state != "ACTIVE" {
			t.Errorf("%s status=%s", team, state)
		}
		if tips[ip] {
			t.Errorf("duplicate IP %s", ip)
		}
		tips[ip] = true
	}
	syncMu.Lock()
	calls := syncCalls
	syncMu.Unlock()
	if calls >= n {
		t.Fatalf("sync ran %d times for %d provisions; expected coalescing", calls, n)
	}
}

func TestSyncCoalescingFailureWithWaiters(t *testing.T) {
	provisionTestSetup(t, &MockProvisioner{})
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	gatewaySyncFunc = func(bool) (bool, string) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		if call <= 2 {
			return false, "sync failed"
		}
		return true, "sync recovered"
	}
	type result struct {
		ok  bool
		msg string
	}
	results := make(chan result, 4)
	callSync := func() {
		ok, msg := runCoalescedProvisionSync(context.Background())
		results <- result{ok: ok, msg: msg}
	}
	go callSync()
	<-firstStarted
	for i := 0; i < 3; i++ {
		go callSync()
	}
	deadline := time.Now().Add(time.Second)
	for {
		provisionSync.mu.Lock()
		waiters := provisionSync.waiters
		provisionSync.mu.Unlock()
		if waiters == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters=%d, want 3", waiters)
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseFirst)
	for i := 0; i < 4; i++ {
		got := <-results
		if got.ok || got.msg != "sync failed" {
			t.Errorf("coalesced caller got (%v, %q), want (false, %q)", got.ok, got.msg, "sync failed")
		}
	}
	provisionSync.mu.Lock()
	running, pending := provisionSync.running, provisionSync.pending
	provisionSync.mu.Unlock()
	if running {
		t.Fatal("sync still marked running after completion")
	}
	if pending {
		t.Fatal("pending flag not reset after completion")
	}

	ok, msg := runCoalescedProvisionSync(context.Background())
	if !ok || msg != "sync recovered" {
		t.Fatalf("subsequent sync got (%v, %q), want (true, %q)", ok, msg, "sync recovered")
	}
	callsMu.Lock()
	gotCalls := calls
	callsMu.Unlock()
	if gotCalls != 3 {
		t.Fatalf("sync calls=%d, want 3 (two coalesced failures and one fresh success)", gotCalls)
	}
}

func TestProvisionContextCancellationDoesNotLeakResources(t *testing.T) {
	mock := &MockProvisioner{Statuses: []string{"BUILD"}}
	provisionTestSetup(t, mock)
	provisionTimeout = 10 * time.Millisecond
	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/provision", bytes.NewBufferString(`{"team":"cancel-me"}`))
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handleProvision(rec, req)
	if rec.Code != http.StatusGatewayTimeout && rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rec.Code)
	}
}
