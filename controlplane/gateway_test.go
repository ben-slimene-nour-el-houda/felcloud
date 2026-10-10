package main

import (
	"context"
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
