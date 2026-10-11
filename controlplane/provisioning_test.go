package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	provisionNameLocks = newProvisionNameLockMap()
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

func TestProvisionSandboxOwnershipCanBeTornDown(t *testing.T) {
	mock := &MockProvisioner{}
	provisionTestSetup(t, mock)
	rec := callProvision("owned-demo")
	if rec.Code != http.StatusCreated {
		t.Fatalf("provision status=%d body=%s", rec.Code, rec.Body.String())
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	var managedBy string
	if err := db.QueryRow(`SELECT managed_by FROM sandboxes WHERE sandbox_id='owned-demo'`).Scan(&managedBy); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if managedBy != "control-plane" {
		t.Fatalf("managed_by=%q, want control-plane", managedBy)
	}
	result, err := teardownSandbox(context.Background(), "owned-demo")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "RELEASED" {
		t.Fatalf("teardown result=%+v", result)
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

func TestProvisionRetriesNeutronFixedIPConflicts(t *testing.T) {
	mock := &MockProvisioner{ConflictIPs: map[string]bool{"10.20.30.2": true}}
	provisionTestSetup(t, mock)
	rec := callProvision("collision-once")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s, want 201 after retry", rec.Code, rec.Body.String())
	}
	var response provisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.FixedIP == "10.20.30.2" {
		t.Fatalf("provision reused conflicting IP %s", response.FixedIP)
	}
	if mock.Created != 2 {
		t.Fatalf("CreateSandbox calls=%d, want collision then success", mock.Created)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var owner, entityType, status string
	if err := db.QueryRow(`SELECT entity_name,entity_type,status FROM ip_allocations WHERE ip_address='10.20.30.2'`).Scan(&owner, &entityType, &status); err != nil {
		t.Fatal(err)
	}
	if owner != "neutron-conflict-10.20.30.2" || entityType != "EXTERNAL" || status != "RESERVED" {
		t.Fatalf("collision reservation owner=%q type=%q status=%q", owner, entityType, status)
	}
	var retries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE sandbox_id='collision-once' AND type='IP_CONFLICT_RETRY'`).Scan(&retries); err != nil || retries != 1 {
		t.Fatalf("retry events=%d err=%v, want exactly one", retries, err)
	}
}

func TestProvisionConflictRetriesExhaustWithoutLeakedResources(t *testing.T) {
	mock := &MockProvisioner{ConflictIPs: map[string]bool{
		"10.20.30.2": true, "10.20.30.3": true, "10.20.30.4": true,
	}}
	provisionTestSetup(t, mock)
	rec := callProvision("collision-exhausted")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want 502", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["step"] != "ip_conflict" {
		t.Fatalf("failure step=%v body=%s", body["step"], rec.Body.String())
	}
	state, _ := leaseStatus(t, "collision-exhausted")
	if state != "FAILED" || mock.Created != 3 {
		t.Fatalf("state=%s create calls=%d; want FAILED after 3 conflicts", state, mock.Created)
	}
	mock.mu.Lock()
	serverCount, portCount := len(mock.Servers), len(mock.Ports)
	mock.mu.Unlock()
	if serverCount != 0 || portCount != 0 {
		t.Fatalf("leaked resources: servers=%d ports=%d", serverCount, portCount)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var allocated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ip_allocations WHERE entity_name='collision-exhausted' AND status='ALLOCATED'`).Scan(&allocated); err != nil {
		t.Fatal(err)
	}
	if allocated != 0 {
		t.Fatalf("left %d allocated IP leases after exhausting conflicts", allocated)
	}
}

func TestProvisionSkipsIPsAlreadyPresentInNeutron(t *testing.T) {
	mock := &MockProvisioner{Ports: []SandboxPort{{ID: "untracked-port", FixedIPs: []string{"10.20.30.2"}}}}
	provisionTestSetup(t, mock)
	rec := callProvision("existing-neutron-ip")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response provisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.FixedIP == "10.20.30.2" {
		t.Fatalf("allocator selected IP already present in Neutron: %s", response.FixedIP)
	}
}

func TestConcurrentProvisioningWithNeutronConflictsUsesUniqueIPs(t *testing.T) {
	conflicts := make(map[string]bool)
	for i := 2; i <= 4; i++ {
		conflicts[fmt.Sprintf("10.20.30.%d", i)] = true
	}
	mock := &MockProvisioner{ConflictIPs: conflicts}
	provisionTestSetup(t, mock)
	provisionTimeout = 5 * time.Second
	const count = 8
	var wg sync.WaitGroup
	errs := make(chan string, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := callProvision(fmt.Sprintf("collision-parallel-%02d", i))
			if rec.Code != http.StatusCreated {
				errs <- rec.Body.String()
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for message := range errs {
		t.Errorf("parallel provision failed: %s", message)
	}
	ips := make(map[string]string)
	for i := 0; i < count; i++ {
		team := fmt.Sprintf("collision-parallel-%02d", i)
		state, ip := leaseStatus(t, team)
		if state != "ACTIVE" {
			t.Errorf("%s status=%s", team, state)
		}
		if previous, exists := ips[ip]; exists {
			t.Errorf("%s and %s share IP %s", previous, team, ip)
		}
		ips[ip] = team
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
	t.Setenv("PROVISIONER", "")
	t.Setenv("HEALTH_PROBE_MODE", "unsupported")
	if err := configureHealthProbe(); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
	t.Setenv("HEALTH_PROBE_MODE", "skip")
	if err := configureHealthProbe(); err == nil {
		t.Fatal("expected skip mode to be rejected for a real provisioner")
	}
	t.Setenv("PROVISIONER", "mock")
	t.Setenv("HEALTH_PROBE_MODE", "gateway-ssh")
	if err := configureHealthProbe(); err != nil {
		t.Fatalf("mock skip mode rejected: %v", err)
	}
	ok, result := runHealthProbe(context.Background(), "not-an-ip")
	if !ok || result != "health probe skipped (mock provisioner)" {
		t.Fatalf("mock skip probe=(%v,%q)", ok, result)
	}
}

func setupGatewayProbeFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "test_key")
	if err := os.WriteFile(key, []byte("test key material"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(dir, "hosts.yml")
	content := fmt.Sprintf(`all:
  vars:
    ansible_user: test-user
    ansible_ssh_private_key_file: %s
  children:
    gateways:
      hosts:
        gw1:
          ansible_host: 10.20.10.24
        gw2:
          ansible_host: 10.20.10.164
`, key)
	if err := os.WriteFile(inventory, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANSIBLE_INVENTORY", inventory)
	t.Setenv("HEALTH_PROBE_SSH_HOST", "")
	t.Setenv("HEALTH_PROBE_SSH_KEY", "")
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "")
	t.Setenv("FELCLOUD_KNOWN_HOSTS", knownHosts)
	t.Setenv("BASTION_IP", "198.51.100.10")
	t.Setenv("PROVISIONER", "openstack")
	return dir, knownHosts
}

func TestHealthProbeDefaultsToBothGatewaysAndHonorsRequirement(t *testing.T) {
	dir, knownHosts := setupGatewayProbeFixture(t)
	argsLog := filepath.Join(dir, "ssh-args.log")
	sshShim := filepath.Join(dir, "ssh")
	shim := `#!/bin/sh
printf '%s\n' "$*" >> "$SSH_ARGS_LOG"
case "$*" in
  *10.20.10.164*)
    if [ "$SSH_FAIL_GW2" = "1" ]; then printf 503; else printf 200; fi
    ;;
  *) printf 200 ;;
esac
`
	if err := os.WriteFile(sshShim, []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SSH_ARGS_LOG", argsLog)
	t.Setenv("SSH_FAIL_GW2", "1")
	t.Setenv("HEALTH_PROBE_MODE", "")
	t.Setenv("HEALTH_PROBE_REQUIRE", "")
	if err := configureHealthProbe(); err != nil {
		t.Fatal(err)
	}
	if healthProbeMode != "gateway-ssh" {
		t.Fatalf("default mode=%q, want gateway-ssh", healthProbeMode)
	}
	result, err := runHealthProbeAttempt(context.Background(), healthProbeMode, "10.20.30.40")
	if err != nil || probeHTTPStatusOK(result) || result != "503" {
		t.Fatalf("all-gateway probe=(%q,%v), want failure on gw2", result, err)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ssh called %d times, want once per gateway: %s", len(lines), args)
	}
	for _, line := range lines {
		for _, required := range []string{"-J test-user@198.51.100.10", "BatchMode=yes", "ConnectTimeout=3", "StrictHostKeyChecking=accept-new", "UserKnownHostsFile=" + knownHosts} {
			if !strings.Contains(line, required) {
				t.Errorf("ssh args %q missing %q", line, required)
			}
		}
		if strings.Contains(line, "StrictHostKeyChecking=no") {
			t.Errorf("unsafe SSH host-key option in args: %s", line)
		}
	}

	if err := os.WriteFile(argsLog, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEALTH_PROBE_REQUIRE", "any")
	if err := configureHealthProbe(); err != nil {
		t.Fatal(err)
	}
	result, err = runHealthProbeAttempt(context.Background(), healthProbeMode, "10.20.30.40")
	if err != nil || !probeHTTPStatusOK(result) || result != "200" {
		t.Fatalf("any-gateway probe=(%q,%v), want success via gw1", result, err)
	}
	args, err = os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(args), "\n"); got != 2 {
		t.Fatalf("any policy probed %d gateways, want both", got)
	}
}

func TestHealthProbeStartupRequiresResolvableSSHSettings(t *testing.T) {
	t.Setenv("PROVISIONER", "openstack")
	t.Setenv("HEALTH_PROBE_MODE", "gateway-ssh")
	t.Setenv("HEALTH_PROBE_SSH_HOST", "")
	t.Setenv("HEALTH_PROBE_SSH_KEY", "")
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "")
	t.Setenv("ANSIBLE_INVENTORY", filepath.Join(t.TempDir(), "missing.yml"))
	if err := configureHealthProbe(); err == nil || !strings.Contains(err.Error(), "Ansible inventory") {
		t.Fatalf("missing inventory error=%v, want actionable inventory error", err)
	}
	dir, _ := setupGatewayProbeFixture(t)
	t.Setenv("ANSIBLE_INVENTORY", filepath.Join(dir, "hosts.yml"))
	t.Setenv("HEALTH_PROBE_SSH_KEY", filepath.Join(dir, "missing_key"))
	if err := configureHealthProbe(); err == nil || !strings.Contains(err.Error(), "SSH key") {
		t.Fatalf("missing key error=%v, want actionable SSH key error", err)
	}
	t.Setenv("HEALTH_PROBE_SSH_KEY", filepath.Join(dir, "test_key"))
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "invalid host")
	if err := configureHealthProbe(); err == nil || !strings.Contains(err.Error(), "JUMP") {
		t.Fatalf("invalid jump error=%v, want actionable jump-host error", err)
	}
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "")
	t.Setenv("BASTION_IP", "")
	if err := configureHealthProbe(); err == nil || !strings.Contains(err.Error(), "jump host is unresolved") {
		t.Fatalf("missing jump error=%v, want actionable jump-host error", err)
	}
	t.Setenv("BASTION_IP", "198.51.100.10")
	t.Setenv("HEALTH_PROBE_SSH_JUMP", "test-user@198.51.100.10")
	t.Setenv("HEALTH_PROBE_MODE", "direct")
	if err := configureHealthProbe(); err != nil || healthProbeMode != "direct" {
		t.Fatalf("explicit direct mode=(%q,%v), want direct", healthProbeMode, err)
	}
}

func TestMockOnlyProvisioningHooks(t *testing.T) {
	oldProvisioner, oldSync, oldTimeout, oldSemaphore, oldMode := provisioner, gatewaySyncFunc, provisionTimeout, provisionSemaphore, healthProbeMode
	t.Cleanup(func() {
		provisioner, gatewaySyncFunc, provisionTimeout, provisionSemaphore, healthProbeMode = oldProvisioner, oldSync, oldTimeout, oldSemaphore, oldMode
	})

	t.Setenv("PROVISIONER", "mock")
	t.Setenv("HEALTH_PROBE_MODE", "gateway-ssh")
	if err := configureProvisioner(context.Background()); err != nil {
		t.Fatalf("configure mock provisioner: %v", err)
	}
	if healthProbeMode != "skip" {
		t.Fatalf("mock health probe mode=%q, want skip", healthProbeMode)
	}
	if ok, message := gatewaySyncFunc(false); !ok || message != "mock gateway sync" {
		t.Fatalf("mock gateway sync=(%v, %q)", ok, message)
	}

	t.Setenv("PROVISIONER", "")
	t.Setenv("HEALTH_PROBE_MODE", "skip")
	if err := configureProvisioner(context.Background()); err == nil {
		t.Fatal("real provisioner unexpectedly accepted mock-only health/sync hooks")
	}
}

func TestLoadHealthProbeSSHConfigFromInventory(t *testing.T) {
	_, knownHosts := setupGatewayProbeFixture(t)
	t.Setenv("BASTION_IP", "198.51.100.10")
	cfg, err := loadHealthProbeSSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Gateways) != 2 ||
		cfg.Gateways[0] != (healthProbeGateway{Name: "gw1", Host: "10.20.10.24"}) ||
		cfg.Gateways[1] != (healthProbeGateway{Name: "gw2", Host: "10.20.10.164"}) ||
		cfg.User != "test-user" || cfg.Jump != "test-user@198.51.100.10" {
		t.Fatalf("inventory SSH settings = %+v", cfg)
	}
	if filepath.Base(cfg.Key) != "test_key" || cfg.KnownHosts != knownHosts || cfg.Require != "all" {
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

func TestPersistProvisioningAndResourcesRejectStateRaces(t *testing.T) {
	provisionTestSetup(t, &MockProvisioner{})
	lease, err := allocateProvisionLease("state-race", "state-race.cstam.felcloud.tn")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistProvisioning("state-race", lease); err != nil {
		t.Fatal(err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET status='FAILED' WHERE sandbox_id='state-race'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err := persistResources("state-race", &SandboxResources{ServerID: "server", PortID: "port", FixedIP: lease.IPAddress}); err == nil {
		t.Fatal("persistResources accepted a row that left PROVISIONING")
	}
	if err := persistActive("state-race", &SandboxResources{ServerID: "server", PortID: "port", FixedIP: lease.IPAddress}, 1); err == nil {
		t.Fatal("persistActive accepted a row that left PROVISIONING")
	}
}

func TestProvisionFailureReportsMeasuredPartialTimings(t *testing.T) {
	provisionTestSetup(t, &MockProvisioner{})
	gatewaySyncFunc = func(bool) (bool, string) { return false, "injected sync failure" }
	rec := callProvision("timed-failure")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Timings map[string]int64 `json:"timings_ms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"allocate", "port", "server_active", "health", "sync", "total"} {
		if _, ok := body.Timings[key]; !ok {
			t.Errorf("missing %s timing in %v", key, body.Timings)
		}
	}
	if body.Timings["port"] != 11 || body.Timings["server_active"] < 22 || body.Timings["total"] < body.Timings["server_active"] {
		t.Fatalf("partial timings are not measured: %+v", body.Timings)
	}
}

func TestConfigureMockFailureInjection(t *testing.T) {
	oldStep, oldPrefix, oldDelay := mockFailStep, mockFailTeamPrefix, mockProvisionDelay
	t.Cleanup(func() { mockFailStep, mockFailTeamPrefix, mockProvisionDelay = oldStep, oldPrefix, oldDelay })
	t.Setenv("PROVISIONER", "mock")
	t.Setenv("MOCK_FAIL_STEP", "health")
	t.Setenv("MOCK_FAIL_TEAM_PREFIX", "demo-")
	t.Setenv("MOCK_PROVISION_DELAY_MS", "12")
	if err := configureMockFailures(os.Getenv); err != nil {
		t.Fatal(err)
	}
	if !mockFailureMatches("demo-team", "health") || mockFailureMatches("other-team", "health") {
		t.Fatal("mock team-prefix failure filter is incorrect")
	}
	if mockProvisionDelay != 12*time.Millisecond {
		t.Fatalf("mock delay=%s, want 12ms", mockProvisionDelay)
	}
	t.Setenv("MOCK_FAIL_STEP", "unexpected")
	if err := configureMockFailures(os.Getenv); err == nil {
		t.Fatal("invalid MOCK_FAIL_STEP was accepted")
	}
}

func TestProvisionNameLocksAreReleasedAfterUse(t *testing.T) {
	provisionNameLocks = newProvisionNameLockMap()
	for i := 0; i < 1000; i++ {
		unlock := lockProvisionName(fmt.Sprintf("lock-%d", i))
		unlock()
	}
	if got := provisionNameLocks.Len(); got != 0 {
		t.Fatalf("provision name lock map retained %d entries after use", got)
	}
}

func TestReaperRecoversStaleProvisioningResources(t *testing.T) {
	mock := &MockProvisioner{}
	provisionTestSetup(t, mock)
	lease, err := allocateProvisionLease("stale-provision", "stale-provision.cstam.felcloud.tn")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistProvisioning("stale-provision", lease); err != nil {
		t.Fatal(err)
	}
	resources, err := mock.CreateSandbox(context.Background(), CreateReq{Team: "stale-provision", FixedIP: lease.IPAddress})
	if err != nil {
		t.Fatal(err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sandboxes SET updated_at='2000-01-01 00:00:00' WHERE sandbox_id='stale-provision'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	result := reaperTick(db, nowUnix())
	db.Close()
	if result.FailedCount != 1 {
		t.Fatalf("reaper result=%+v, want one recovered provisioning sandbox", result)
	}
	state, _ := leaseStatus(t, "stale-provision")
	if state != "FAILED" {
		t.Fatalf("recovered sandbox state=%s, want FAILED", state)
	}
	mock.mu.Lock()
	serverPresent, portPresent := mock.Servers[resources.ServerID], false
	for _, port := range mock.Ports {
		portPresent = portPresent || port.ID == resources.PortID
	}
	mock.mu.Unlock()
	if serverPresent || portPresent {
		t.Fatalf("crash recovery left resources: server=%v port=%v", serverPresent, portPresent)
	}
	db, err = getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var leaseStatus, eventCount string
	if err := db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name='stale-provision'`).Scan(&leaseStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE sandbox_id='stale-provision' AND type='PROVISION_RECOVERED'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if leaseStatus != "QUARANTINED" || eventCount != "1" {
		t.Fatalf("lease=%s recovery events=%s", leaseStatus, eventCount)
	}
}
