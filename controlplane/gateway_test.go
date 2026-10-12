package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewaySyncDoesNotModifyTrackedVarsFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failRun bool
	}{
		{name: "success"},
		{name: "failure", failRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestDB(t)
			oldVarsPath, oldDomain := varsYmlPath, routingDomain
			oldValidator, oldRunner := gatewayConfigValidator, gatewayPlaybookRunner
			oldDeploy, oldHealth, oldFactory := gatewayDeploymentRunner, gatewayHealthCheck, gatewayStoreFactory
			oldWindow := os.Getenv("HEALTH_REGRESSION_WINDOW_SECONDS")
			t.Cleanup(func() {
				varsYmlPath, routingDomain = oldVarsPath, oldDomain
				gatewayConfigValidator, gatewayPlaybookRunner = oldValidator, oldRunner
				gatewayDeploymentRunner, gatewayHealthCheck, gatewayStoreFactory = oldDeploy, oldHealth, oldFactory
				_ = os.Setenv("HEALTH_REGRESSION_WINDOW_SECONDS", oldWindow)
			})

			varsYmlPath = filepath.Join(t.TempDir(), "vars.yml")
			original := []byte("# keep comments and hand-managed teams\nrouting_domain: custom.example\nteams:\n  - name: preserved\n    ip: 192.0.2.7\n")
			if err := os.WriteFile(varsYmlPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			routingDomain = "custom.example"
			if _, err := allocateLease("sync-team", "sync-team.custom.example", "sandbox", "10.20.30.73"); err != nil {
				t.Fatal(err)
			}

			gatewayConfigValidator = func(string) (bool, string) { return true, "valid" }
			_ = os.Setenv("HEALTH_REGRESSION_WINDOW_SECONDS", "0")
			db, err := getDBConnection()
			if err != nil {
				t.Fatal(err)
			}
			gatewayStoreFactory = func() (*gatewayStore, error) { return &gatewayStore{db: db}, nil }
			deployed := ""
			gatewayDeploymentRunner = func(genID, snapshot, active, standby string) (gatewayDeployment, error) {
				deployed = snapshot
				if tc.failRun {
					return gatewayDeployment{}, os.ErrInvalid
				}
				return gatewayDeployment{Generation: genID, GW1Backup: "/backup/1", GW2Backup: "/backup/2"}, nil
			}
			baseline := gatewayHealthSnapshot{Probes: ProbeResult{VIPHolder: "gw1"}, VIPStatus: 200, Backends: map[string]string{}}
			gatewayHealthCheck = func(context.Context, *gatewayStore) (gatewayHealthSnapshot, error) { return baseline, nil }
			ok, _ := syncHAProxyDualGateways(false)
			if ok == tc.failRun {
				t.Fatalf("sync success=%v, want %v", ok, !tc.failRun)
			}
			after, err := os.ReadFile(varsYmlPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(original) {
				t.Fatalf("sync modified vars.yml\nbefore: %q\nafter:  %q", original, after)
			}
			if !strings.Contains(deployed, "sync-team") || !strings.Contains(deployed, "10.20.30.73") || !strings.Contains(deployed, "custom.example") {
				t.Fatalf("candidate snapshot missing provisioned route: %s", deployed)
			}
		})
	}
}

func TestGatewayConfigForHostAddsGatewayIdentityToPublicFrontends(t *testing.T) {
	base := "frontend http_front\n    mode http\n    bind *:80\n\nfrontend https_front\n    mode http\n    bind *:443\n"
	for _, host := range []string{"gw1", "gw2"} {
		got := gatewayConfigForHost(base, host)
		if strings.Count(got, "http-response set-header X-Gateway-ID "+host) != 2 {
			t.Fatalf("expected public HTTP and HTTPS headers for %s, got:\n%s", host, got)
		}
	}
	withPlaceholder := "frontend http_front\n    mode http\n    http-response set-header X-Gateway-ID __FELCLOUD_GATEWAY_ID__\n"
	if got := gatewayConfigForHost(withPlaceholder, "gw2"); strings.Count(got, "X-Gateway-ID gw2") != 1 {
		t.Fatalf("placeholder header was not safely replaced: %s", got)
	}
}

func TestGeneratedRoutesExcludeNonReadySandboxes(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, sandbox := range []struct {
		id, ip, status string
	}{
		{"route-active", "10.20.30.90", "ACTIVE"},
		{"route-provisioning", "10.20.30.91", "PROVISIONING"},
		{"route-failed", "10.20.30.92", "FAILED"},
		{"route-tearing-down", "10.20.30.93", "TEARING_DOWN"},
		{"route-released", "10.20.30.94", "RELEASED"},
	} {
		if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id,hostname,subnet_name,private_ip,status)
			VALUES(?,?,'sandbox',?,?)`, sandbox.id, sandbox.id+".cstam.felcloud.tn", sandbox.ip, sandbox.status); err != nil {
			t.Fatal(err)
		}
	}

	teams, err := updateAnsibleTeams()
	if err != nil {
		t.Fatal(err)
	}
	config := generateHAProxyConfig(teams)
	if len(teams) != 1 || teams[0].Name != "route-active" {
		t.Fatalf("generated route entries=%+v, want only the ACTIVE sandbox", teams)
	}
	for _, forbidden := range []string{"route-provisioning", "route-failed", "route-tearing-down", "route-released"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("HAProxy config contains non-ready sandbox %q:\n%s", forbidden, config)
		}
	}
}

func TestParseBackendStatesKeepsGatewayIdentity(t *testing.T) {
	output := "gw1 | CHANGED | rc=0 >>\n# pxname, svname, status\nteam-a_backend, team-a_srv, UP 2/3\n\ngw2 | CHANGED | rc=0 >>\n# pxname, svname, status\nteam-a_backend, team-a_srv, DOWN 0/3\n"
	got := parseBackendStates(output)
	if !strings.HasPrefix(got["gw1/team-a_backend/team-a_srv"], "UP") || !strings.HasPrefix(got["gw2/team-a_backend/team-a_srv"], "DOWN") {
		t.Fatalf("unexpected per-gateway backend states: %#v", got)
	}
}

func TestHealthWindowDetectsBackendBecomingDown(t *testing.T) {
	oldCheck := gatewayHealthCheck
	oldWindow := os.Getenv("HEALTH_REGRESSION_WINDOW_SECONDS")
	t.Cleanup(func() { gatewayHealthCheck = oldCheck; _ = os.Setenv("HEALTH_REGRESSION_WINDOW_SECONDS", oldWindow) })
	_ = os.Setenv("HEALTH_REGRESSION_WINDOW_SECONDS", "0")
	gatewayHealthCheck = func(context.Context, *gatewayStore) (gatewayHealthSnapshot, error) {
		return gatewayHealthSnapshot{VIPStatus: 200, Backends: map[string]string{"gw1/team-a/srv": "DOWN 0/2"}}, nil
	}
	err := verifyGatewayHealthWindow(nil, gatewayHealthSnapshot{Backends: map[string]string{"gw1/team-a/srv": "UP 2/2"}})
	if err == nil || !strings.Contains(err.Error(), "became DOWN") {
		t.Fatalf("expected backend regression, got %v", err)
	}
}

func TestValidateHAProxySyntaxRejectsMissingFrontendAndBackend(t *testing.T) {
	valid, message := validateHAProxySyntax("global\n    log /dev/log local0\n")
	if valid {
		t.Fatalf("validator accepted a config without frontend/backend definitions: %s", message)
	}
}

func TestGatewayRateLimitingAndSmartFallbackConfig(t *testing.T) {
	teams := []TeamEntry{
		{Name: "team-test", IP: "10.20.30.50", Port: 80},
	}
	cfg := generateHAProxyConfig(teams)
	if !strings.Contains(cfg, "stick-table type ip size 100k") {
		t.Errorf("HAProxy config missing rate limiting stick-table")
	}
	if !strings.Contains(cfg, "http-request deny deny_status 429") {
		t.Errorf("HAProxy config missing 429 rate limit denial")
	}
	if !strings.Contains(cfg, "503 • SANDBOX OFFLINE") || !strings.Contains(cfg, "default_maintenance") {
		t.Errorf("HAProxy config missing smart maintenance/fallback page")
	}
}

func TestCustomDomainBindingHAProxyRoutes(t *testing.T) {
	teams := []TeamEntry{
		{Name: "team-custom", IP: "10.20.30.51", Port: 80, CustomDomain: "app.team-custom.org"},
		{Name: "team-default", IP: "10.20.30.52", Port: 80},
	}
	cfg := generateHAProxyConfig(teams)
	if !strings.Contains(cfg, "acl is_team-custom_custom hdr(host) -i app.team-custom.org") {
		t.Errorf("HAProxy config missing custom domain ACL")
	}
	if !strings.Contains(cfg, "use_backend team-custom_backend if is_team-custom || is_team-custom_custom") {
		t.Errorf("HAProxy config missing custom domain backend routing rule")
	}
	if strings.Contains(cfg, "is_team-default_custom") {
		t.Errorf("HAProxy config unexpectedly added custom ACL for team without custom domain")
	}
}

func TestCustomDomainAPIHandler(t *testing.T) {
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id, hostname, subnet_name, private_ip, status)
		VALUES('team-domain-test', 'team-domain-test.cstam.felcloud.tn', 'sandbox', '10.20.30.60', 'ACTIVE')`); err != nil {
		t.Fatal(err)
	}

	// 1. Invalid custom domain format
	badBody := `{"sandbox_id":"team-domain-test","custom_domain":"invalid domain!"}`
	req := httptest.NewRequest("POST", "/api/sandboxes/custom-domain", strings.NewReader(badBody))
	w := httptest.NewRecorder()
	handleCustomDomain(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid domain, got %d", w.Code)
	}

	// 2. Valid custom domain
	goodBody := `{"sandbox_id":"team-domain-test","custom_domain":"custom.team.example.com"}`
	req = httptest.NewRequest("POST", "/api/sandboxes/custom-domain", strings.NewReader(goodBody))
	w = httptest.NewRecorder()
	handleCustomDomain(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid custom domain, got %d: %s", w.Code, w.Body.String())
	}

	var customDomain string
	if err := db.QueryRow("SELECT COALESCE(custom_domain,'') FROM sandboxes WHERE sandbox_id = 'team-domain-test'").Scan(&customDomain); err != nil {
		t.Fatal(err)
	}
	if customDomain != "custom.team.example.com" {
		t.Fatalf("expected custom_domain='custom.team.example.com', got %q", customDomain)
	}
}

