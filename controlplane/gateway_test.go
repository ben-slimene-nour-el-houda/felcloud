package main

import (
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
			t.Cleanup(func() {
				varsYmlPath, routingDomain = oldVarsPath, oldDomain
				gatewayConfigValidator, gatewayPlaybookRunner = oldValidator, oldRunner
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
			var gotRoutes, generatedPath string
			gatewayPlaybookRunner = func(routesVarsPath string) (string, error) {
				generatedPath = routesVarsPath
				generated, err := os.ReadFile(routesVarsPath)
				if err != nil {
					return "", err
				}
				gotRoutes = string(generated)
				if tc.failRun {
					return "simulated Ansible failure", os.ErrInvalid
				}
				return "ok", nil
			}
			ok, _ := syncHAProxyDualGateways(false)
			if ok == tc.failRun {
				t.Fatalf("sync success=%v, want %v", ok, !tc.failRun)
			}
			if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
				t.Fatalf("generated routes file still exists after sync: stat err=%v", err)
			}
			after, err := os.ReadFile(varsYmlPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(original) {
				t.Fatalf("sync modified vars.yml\nbefore: %q\nafter:  %q", original, after)
			}
			if !strings.Contains(gotRoutes, "sync-team") || !strings.Contains(gotRoutes, "10.20.30.73") || !strings.Contains(gotRoutes, "custom.example") {
				t.Fatalf("generated routes vars missing provisioned route: %s", gotRoutes)
			}
		})
	}
}
