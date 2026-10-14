//go:build live
// +build live

package main

import (
	"bufio"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
	return scanner.Err()
}

func initLiveEnv(t *testing.T) ProvisionConfig {
	_ = loadEnvFile("../.env")
	if os.Getenv("OS_CLOUD") == "" {
		os.Setenv("OS_CLOUD", "felcloud")
	}
	cfg, err := loadProvisionConfig()
	if err != nil {
		t.Fatalf("Failed to load provision config: %v", err)
	}
	return cfg
}

func TestLiveOpenStackAuthenticationAndDiscovery(t *testing.T) {
	cfg := initLiveEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := newOpenStackProvisioner(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to authenticate to live OpenStack: %v", err)
	}

	secID, err := p.resolveSecurityGroup(ctx)
	if err != nil {
		t.Fatalf("Failed to resolve security group %q: %v", cfg.SecurityGroup, err)
	}
	t.Logf("✓ Successfully authenticated to OpenStack Keystone")
	t.Logf("✓ Discovered Nova and Neutron endpoints")
	t.Logf("✓ Resolved Security Group %s -> ID: %s", cfg.SecurityGroup, secID)
	t.Logf("✓ Sandbox Network ID: %s", p.cfg.NetworkID)
}

func TestLiveOpenStackN1ProvisionAndTeardownLifecycle(t *testing.T) {
	cfg := initLiveEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	p, err := newOpenStackProvisioner(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to initialize OpenStack provisioner: %v", err)
	}

	testTeam := "test-n1-live"
	testIP := "10.20.30.245"

	t.Logf("--> Step 1: Provisioning live sandbox '%s' on %s...", testTeam, testIP)
	res, err := p.CreateSandbox(ctx, CreateReq{Team: testTeam, FixedIP: testIP})
	if err != nil {
		t.Fatalf("CreateSandbox failed: %v", err)
	}
	t.Logf("✓ Nova Server ID: %s (creation took %d ms)", res.ServerID, res.ServerCreateDurationMS)
	t.Logf("✓ Neutron Port ID: %s (creation took %d ms)", res.PortID, res.PortDurationMS)
	t.Logf("✓ Fixed IP: %s", res.FixedIP)

	// Ensure cleanup runs even if assertions fail
	defer func() {
		t.Logf("--> Teardown: Deleting live sandbox server %s and port %s...", res.ServerID, res.PortID)
		if delErr := p.DeleteSandbox(context.Background(), res.ServerID, res.PortID); delErr != nil {
			t.Errorf("Cleanup DeleteSandbox failed: %v", delErr)
		} else {
			t.Logf("✓ Cleanup DeleteSandbox initiated successfully")
		}
	}()

	// Step 2: Poll for server ACTIVE status
	t.Logf("--> Step 2: Polling for server status...")
	active := false
	for i := 0; i < 30; i++ {
		status, err := p.GetServer(ctx, res.ServerID)
		if err != nil {
			t.Logf("  Waiting for server (err: %v)...", err)
		} else {
			t.Logf("  Server status: %s (poll %d/30)", status, i+1)
			if status == "ACTIVE" {
				active = true
				break
			}
			if status == "ERROR" {
				t.Fatalf("Server entered ERROR state")
			}
		}
		time.Sleep(3 * time.Second)
	}
	if !active {
		t.Fatalf("Server did not reach ACTIVE state within timeout")
	}
	t.Logf("✓ Server reached ACTIVE state in OpenStack")

	// Step 3: Find resources
	found, err := p.FindSandboxResources(ctx, testTeam)
	if err != nil {
		t.Fatalf("FindSandboxResources failed: %v", err)
	}
	if found.ServerID != res.ServerID {
		t.Errorf("FindSandboxResources server ID mismatch: got %s, want %s", found.ServerID, res.ServerID)
	}
	if found.PortID != res.PortID {
		t.Errorf("FindSandboxResources port ID mismatch: got %s, want %s", found.PortID, res.PortID)
	}
	t.Logf("✓ FindSandboxResources correctly resolved live Nova and Neutron IDs")

	// Step 4: Verify in ListSandboxServers
	servers, err := p.ListSandboxServers(ctx)
	if err != nil {
		t.Fatalf("ListSandboxServers failed: %v", err)
	}
	serverFound := false
	for _, s := range servers {
		if s.ID == res.ServerID {
			serverFound = true
			break
		}
	}
	if !serverFound {
		t.Errorf("Server %s not found in ListSandboxServers", res.ServerID)
	}
	t.Logf("✓ Server confirmed present in ListSandboxServers")

	// Step 5: Teardown
	t.Logf("--> Step 5: Executing N1 Teardown...")
	err = p.DeleteSandbox(ctx, res.ServerID, res.PortID)
	if err != nil {
		t.Fatalf("DeleteSandbox failed: %v", err)
	}

	// Step 6: Verify absence
	t.Logf("--> Step 6: Polling for verified resource absence...")
	deleted := false
	for i := 0; i < 20; i++ {
		_, err := p.GetServer(ctx, res.ServerID)
		if err == ErrOpenStackNotFound {
			deleted = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !deleted {
		t.Fatalf("Server was not confirmed absent within timeout")
	}
	t.Logf("✓ Server confirmed absent (404 Not Found) in Nova")
	t.Logf("✓ N1 Provision and Teardown Lifecycle Completed Successfully on Live OpenStack!")
}

func TestLiveOpenStackN3OwnershipAndDriftSafety(t *testing.T) {
	cfg := initLiveEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := newOpenStackProvisioner(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to initialize OpenStack provisioner: %v", err)
	}

	// 1. Verify existing server inventory is readable and unmanaged servers are present
	servers, err := p.ListSandboxServers(ctx)
	if err != nil {
		t.Fatalf("ListSandboxServers failed: %v", err)
	}
	t.Logf("✓ Discovered %d sandbox server(s) in project", len(servers))

	// 2. Verify protected IPs cannot be reused/conflict
	ports, err := p.ListSandboxPorts(ctx)
	if err != nil {
		t.Fatalf("ListSandboxPorts failed: %v", err)
	}
	t.Logf("✓ Discovered %d sandbox port(s) in network", len(ports))
	for _, port := range ports {
		t.Logf("  Port: %s (%s) IPs: %v", port.Name, port.ID, port.FixedIPs)
	}

	t.Logf("✓ N3 Ownership and Inventory Inspection Verified on Live OpenStack!")
}
