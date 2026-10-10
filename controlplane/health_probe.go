package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type healthProbeSSHConfig struct {
	Host string
	User string
	Key  string
	Jump string
}

type healthProbeInventory struct {
	All struct {
		Vars struct {
			User string `yaml:"ansible_user"`
			Key  string `yaml:"ansible_ssh_private_key_file"`
		} `yaml:"vars"`
		Children map[string]struct {
			Hosts map[string]struct {
				Address string `yaml:"ansible_host"`
			} `yaml:"hosts"`
		} `yaml:"children"`
	} `yaml:"all"`
}

var healthProbeMode = "direct"
var healthProbeSSH healthProbeSSHConfig
var healthProbeRunner = runHealthProbeAttempt

var safeSSHHostRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var safeSSHUserRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func configureHealthProbe() error {
	if os.Getenv("PROVISIONER") == "mock" {
		healthProbeMode = "skip"
		healthProbeSSH = healthProbeSSHConfig{}
		return nil
	}
	mode := strings.TrimSpace(os.Getenv("HEALTH_PROBE_MODE"))
	if mode == "" {
		mode = "direct"
	}
	if mode == "skip" {
		if os.Getenv("PROVISIONER") != "mock" {
			return fmt.Errorf("HEALTH_PROBE_MODE=skip is allowed only with PROVISIONER=mock")
		}
		healthProbeMode = mode
		healthProbeSSH = healthProbeSSHConfig{}
		return nil
	}
	if mode != "direct" && mode != "gateway-ssh" {
		return fmt.Errorf("HEALTH_PROBE_MODE must be direct, gateway-ssh, or skip with PROVISIONER=mock")
	}
	healthProbeMode = mode
	if mode == "direct" {
		healthProbeSSH = healthProbeSSHConfig{}
		return nil
	}
	settings, err := loadHealthProbeSSHConfig()
	if err != nil {
		return err
	}
	healthProbeSSH = settings
	return nil
}

func loadHealthProbeSSHConfig() (healthProbeSSHConfig, error) {
	inventoryPath := strings.TrimSpace(os.Getenv("ANSIBLE_INVENTORY"))
	if inventoryPath == "" {
		inventoryPath = "../ansible/inventory/hosts.yml"
	}
	if !filepath.IsAbs(inventoryPath) {
		inventoryPath = filepath.Clean(inventoryPath)
	}
	contents, err := os.ReadFile(inventoryPath)
	if err != nil && !filepath.IsAbs(inventoryPath) {
		inventoryPath = filepath.Join("..", inventoryPath)
		contents, err = os.ReadFile(inventoryPath)
	}
	if err != nil {
		return healthProbeSSHConfig{}, fmt.Errorf("read Ansible inventory for gateway health probe: %w", err)
	}
	var inventory healthProbeInventory
	if err := yaml.Unmarshal(contents, &inventory); err != nil {
		return healthProbeSSHConfig{}, fmt.Errorf("parse Ansible inventory for gateway health probe: %w", err)
	}
	group, ok := inventory.All.Children["gateways"]
	if !ok || len(group.Hosts) == 0 {
		return healthProbeSSHConfig{}, fmt.Errorf("Ansible inventory has no gateway hosts for health probe")
	}
	names := make([]string, 0, len(group.Hosts))
	for name := range group.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	address := group.Hosts[names[0]].Address
	if address == "" {
		address = names[0]
	}
	settings := healthProbeSSHConfig{
		Host: address,
		User: inventory.All.Vars.User,
		Key:  inventory.All.Vars.Key,
	}
	if settings.User == "" {
		settings.User = "ansible"
	}
	if jump := strings.TrimSpace(os.Getenv("HEALTH_PROBE_SSH_JUMP")); jump != "" {
		settings.Jump = jump
	} else {
		bastion := strings.TrimSpace(os.Getenv("BASTION_IP"))
		if bastion == "" {
			bastion = "203.0.113.119"
		}
		settings.Jump = settings.User + "@" + bastion
	}
	if host := strings.TrimSpace(os.Getenv("HEALTH_PROBE_SSH_HOST")); host != "" {
		settings.Host = host
	}
	keyOverride := strings.TrimSpace(os.Getenv("HEALTH_PROBE_SSH_KEY"))
	if keyOverride != "" {
		settings.Key = keyOverride
	}
	if settings.Key == "" {
		return healthProbeSSHConfig{}, fmt.Errorf("Ansible inventory has no SSH key for gateway health probe")
	}
	keyUsesInventoryDir := strings.Contains(settings.Key, "{{ inventory_dir }}")
	if keyUsesInventoryDir {
		settings.Key = strings.ReplaceAll(settings.Key, "{{ inventory_dir }}", filepath.Dir(inventoryPath))
		settings.Key = strings.TrimSpace(settings.Key)
	}
	if !filepath.IsAbs(settings.Key) && !keyUsesInventoryDir && keyOverride == "" {
		settings.Key = filepath.Join(filepath.Dir(inventoryPath), settings.Key)
	}
	settings.Key, err = filepath.Abs(settings.Key)
	if err != nil {
		return healthProbeSSHConfig{}, fmt.Errorf("resolve SSH key path: %w", err)
	}
	if !safeSSHHostRE.MatchString(settings.Host) || !safeSSHUserRE.MatchString(settings.User) {
		return healthProbeSSHConfig{}, fmt.Errorf("invalid gateway SSH host or user")
	}
	if !safeJumpTarget(settings.Jump) {
		return healthProbeSSHConfig{}, fmt.Errorf("invalid HEALTH_PROBE_SSH_JUMP")
	}
	return settings, nil
}

func safeJumpTarget(jump string) bool {
	parts := strings.Split(jump, "@")
	if len(parts) == 1 {
		return safeSSHHostRE.MatchString(parts[0])
	}
	return len(parts) == 2 && safeSSHUserRE.MatchString(parts[0]) && safeSSHHostRE.MatchString(parts[1])
}

func runHealthProbeAttempt(ctx context.Context, mode, ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil || parsed.String() != ip {
		return "", fmt.Errorf("probe target must be a literal IPv4 address")
	}
	if mode == "direct" {
		return directHealthProbeAttempt(ctx, ip)
	}
	if mode == "gateway-ssh" {
		return sshHealthProbeAttempt(ctx, ip, healthProbeSSH)
	}
	return "", fmt.Errorf("unsupported health probe mode %q", mode)
}

func directHealthProbeAttempt(ctx context.Context, ip string) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ip+":80/", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP probe: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return fmt.Sprintf("%d", resp.StatusCode), nil
}

func sshHealthProbeAttempt(ctx context.Context, ip string, cfg healthProbeSSHConfig) (string, error) {
	if cfg.Host == "" || cfg.User == "" || cfg.Key == "" || !safeJumpTarget(cfg.Jump) {
		return "", fmt.Errorf("gateway SSH probe is not configured")
	}
	remoteCommand := "curl -m 3 -s -o /dev/null -w '%{http_code}' http://" + ip + ":80/"
	args := []string{"-i", cfg.Key, "-J", cfg.Jump, "-o", "BatchMode=yes", "-o", "ConnectTimeout=3", "--", cfg.User + "@" + cfg.Host, remoteCommand}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("SSH error: %s", message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func probeHTTPStatusOK(result string) bool {
	if len(result) != 3 || result[0] < '2' || result[0] > '3' {
		return false
	}
	return result[1] >= '0' && result[1] <= '9' && result[2] >= '0' && result[2] <= '9'
}
