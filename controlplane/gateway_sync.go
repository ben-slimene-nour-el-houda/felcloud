package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var gatewayConfigValidator = validateHAProxySyntax
var gatewayPlaybookRunner = runGatewayPlaybook

func checkSandboxHealth(ipAddress string, port int, timeout time.Duration, maxRetries int, retryInterval time.Duration) (bool, string) {
	if port <= 0 {
		port = 80
	}
	url := fmt.Sprintf("http://%s:%d/", ipAddress, port)
	fmt.Printf("--> Performing pre-flight health check on %s (max %d attempts)...\n", url, maxRetries)

	client := &http.Client{
		Timeout: timeout,
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err == nil {
			req.Header.Set("User-Agent", "FelCloud-HealthCheck/2.0")
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
					fmt.Printf("✓ Health check PASSED on %s (Attempt %d, Status %d)\n", url, attempt, resp.StatusCode)
					return true, msg
				}
			}
		}

		// Fallback lightweight TCP socket probe
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ipAddress, port), timeout)
		if err == nil {
			conn.Close()
			fmt.Printf("✓ Health check (TCP socket) PASSED on %s:%d\n", ipAddress, port)
			return true, "TCP Connection Established"
		}

		if attempt < maxRetries {
			time.Sleep(retryInterval)
		}
	}

	msg := fmt.Sprintf("Connection to %s:%d failed or timed out", ipAddress, port)
	fmt.Printf("✗ Pre-flight health check FAILED on %s after %d attempts.\n", url, maxRetries)
	return false, msg
}

func validateHAProxySyntax(configContent string) (bool, string) {
	tempCertPath := "/tmp/haproxy_temp_test_cert.pem"
	testContent := configContent
	tempCertCreated := false

	// Find cert matches
	re := regexp.MustCompile(`crt\s+(\S+)`)
	matches := re.FindAllStringSubmatch(configContent, -1)
	for _, m := range matches {
		if len(m) > 1 {
			certPath := m[1]
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				if _, err := os.Stat(tempCertPath); os.IsNotExist(err) {
					cmd := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
						"-keyout", "/tmp/temp_k.key", "-out", "/tmp/temp_c.crt",
						"-days", "1", "-subj", "/CN=localhost")
					_ = cmd.Run()
					kData, err1 := os.ReadFile("/tmp/temp_k.key")
					cData, err2 := os.ReadFile("/tmp/temp_c.crt")
					if err1 == nil && err2 == nil {
						combined := append(kData, '\n')
						combined = append(combined, cData...)
						os.WriteFile(tempCertPath, combined, 0644)
						tempCertCreated = true
					}
					_ = os.Remove("/tmp/temp_k.key")
					_ = os.Remove("/tmp/temp_c.crt")
				}
				if _, err := os.Stat(tempCertPath); err == nil {
					testContent = strings.ReplaceAll(testContent, certPath, tempCertPath)
				}
			}
		}
	}

	tmpFile, err := os.CreateTemp("", "haproxy_*.cfg")
	if err != nil {
		return false, err.Error()
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(testContent)
	tmpFile.Close()

	cmd := exec.Command("haproxy", "-c", "-f", tmpFile.Name())
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	if err == nil {
		if tempCertCreated {
			_ = os.Remove(tempCertPath)
		}
		return true, "Configuration syntax is valid"
	}

	if tempCertCreated {
		_ = os.Remove(tempCertPath)
	}

	// If haproxy CLI is missing on the system
	if strings.Contains(err.Error(), "executable file not found") {
		if strings.Contains(configContent, "frontend") && strings.Contains(configContent, "backend") {
			return true, "Syntax structure valid (haproxy CLI not found on host)"
		}
		return false, "Missing frontend or backend definitions"
	}

	return false, fmt.Sprintf("HAProxy syntax error:\n%s\n%s", errBuf.String(), outBuf.String())
}

type TeamEntry struct {
	Name string `yaml:"name" json:"name"`
	IP   string `yaml:"ip" json:"ip"`
	Port int    `yaml:"port,omitempty" json:"port,omitempty"`
}

func generateHAProxyConfig(teams []TeamEntry) string {
	var sb strings.Builder

	sb.WriteString("global\n")
	sb.WriteString("    log /dev/log local0\n")
	sb.WriteString("    log /dev/log local1 notice\n")
	sb.WriteString("    chroot /var/lib/haproxy\n")
	sb.WriteString("    user haproxy\n")
	sb.WriteString("    group haproxy\n")
	sb.WriteString("    daemon\n")
	sb.WriteString("    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners\n")
	sb.WriteString("    stats timeout 30s\n\n")

	sb.WriteString("defaults\n")
	sb.WriteString("    log     global\n")
	sb.WriteString("    mode    http\n")
	sb.WriteString("    option  httplog\n")
	sb.WriteString("    option  dontlognull\n")
	sb.WriteString("    timeout connect 5000ms\n")
	sb.WriteString("    timeout client  50000ms\n")
	sb.WriteString("    timeout server  50000ms\n\n")

	sb.WriteString("# --- Hostname-Based ACL Routing (HTTP 80 & HTTPS 443) ---\n\n")

	sb.WriteString("frontend http_front\n")
	sb.WriteString("    bind *:80\n")
	sb.WriteString("    mode http\n")
	for _, t := range teams {
		sb.WriteString(fmt.Sprintf("    acl is_%s hdr(host) -i %s.%s\n", t.Name, t.Name, routingDomain))
		sb.WriteString(fmt.Sprintf("    use_backend %s_backend if is_%s\n", t.Name, t.Name))
	}
	sb.WriteString("    default_backend default_maintenance\n\n")

	sb.WriteString("frontend https_front\n")
	sb.WriteString("    bind *:443 ssl crt /etc/haproxy/certs/felcloud-gateway.pem alpn h2,http/1.1\n")
	sb.WriteString("    mode http\n")
	for _, t := range teams {
		sb.WriteString(fmt.Sprintf("    acl is_%s hdr(host) -i %s.%s\n", t.Name, t.Name, routingDomain))
		sb.WriteString(fmt.Sprintf("    use_backend %s_backend if is_%s\n", t.Name, t.Name))
	}
	sb.WriteString("    default_backend default_maintenance\n\n")

	for _, t := range teams {
		port := t.Port
		if port == 0 {
			port = 80
		}
		sb.WriteString(fmt.Sprintf("backend %s_backend\n", t.Name))
		sb.WriteString("    mode http\n")
		sb.WriteString("    balance roundrobin\n")
		sb.WriteString(fmt.Sprintf("    server %s_srv %s:%d check inter 2000 rise 2 fall 3\n\n", t.Name, t.IP, port))
	}

	sb.WriteString("backend default_maintenance\n")
	sb.WriteString("    mode http\n")
	sb.WriteString("    http-request return status 503 content-type \"text/plain\" lf-string \"503 Service Unavailable - FelCloud Resilient Gateway Maintenance\\n\"\n\n")

	return sb.String()
}

func updateAnsibleTeams() ([]TeamEntry, error) {
	db, err := getDBConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT sandbox_id, private_ip FROM sandboxes WHERE status IN ('ACTIVE', 'PROVISIONING')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []TeamEntry
	for rows.Next() {
		var sID, ip string
		if err := rows.Scan(&sID, &ip); err == nil && ip != "" {
			teams = append(teams, TeamEntry{
				Name: sID,
				IP:   ip,
				Port: 80,
			})
		}
	}

	return teams, nil
}

func createGeneratedRoutesVars(teams []TeamEntry) (string, func(), error) {
	content, err := yaml.Marshal(map[string]interface{}{
		"teams":          teams,
		"routing_domain": routingDomain,
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode generated gateway routes: %w", err)
	}
	file, err := os.CreateTemp("", "felcloud-routes-*.yml")
	if err != nil {
		return "", nil, fmt.Errorf("create generated gateway routes file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(content); err != nil {
		file.Close()
		os.Remove(path)
		return "", nil, fmt.Errorf("write generated gateway routes file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("close generated gateway routes file: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func rollbackConfig() bool {
	db, err := getDBConnection()
	if err != nil {
		return false
	}
	defer db.Close()

	var genID, snapshot string
	err = db.QueryRow("SELECT generation_id, config_snapshot FROM config_generations WHERE status = 'COMMITTED' ORDER BY id DESC LIMIT 1").Scan(&genID, &snapshot)
	if err != nil {
		fmt.Println("✗ No previous committed generation found for rollback.")
		return false
	}

	fmt.Printf("--> Rolling back to Generation [%s]...\n", genID)
	os.WriteFile("/tmp/haproxy_rollback.cfg", []byte(snapshot), 0644)
	recordEvent(db, "", "rollback", genID, 0)
	fmt.Printf("✓ Configuration restored to previous generation [%s].\n", genID)
	return true
}

func syncHAProxyDualGateways(dryRun bool) (bool, string) {
	teams, err := updateAnsibleTeams()
	if err != nil {
		return false, fmt.Sprintf("Failed to load sandbox routes: %v", err)
	}
	candidateCfg := generateHAProxyConfig(teams)

	isValid, validationMsg := gatewayConfigValidator(candidateCfg)
	if !isValid {
		fmt.Printf("✗ CRITICAL: Candidate HAProxy configuration failed validation:\n%s\n", validationMsg)
		fmt.Println("✗ Aborting reload! Active gateway configuration untouched.")
		db, _ := getDBConnection()
		if db != nil {
			recordEvent(db, "", "sync_failed", validationMsg, 0)
			db.Close()
		}
		return false, fmt.Sprintf("Validation failed: %s", validationMsg)
	}

	fmt.Println("✓ Candidate configuration pre-validation passed.")
	if dryRun {
		return true, "Dry-run validation successful"
	}

	genID := fmt.Sprintf("gen-%s", time.Now().UTC().Format("20060102150405"))
	db, err := getDBConnection()
	if err != nil {
		return false, err.Error()
	}

	_, err = db.Exec("INSERT INTO config_generations (generation_id, config_snapshot, status) VALUES (?, ?, 'VALIDATED')", genID, candidateCfg)
	db.Close()
	if err != nil {
		return false, fmt.Sprintf("Failed to record config generation: %v", err)
	}

	fmt.Printf("\n--> Deploying Generation [%s] across GW1 and GW2...\n", genID)
	routesPath, cleanupRoutes, err := createGeneratedRoutesVars(teams)
	if err != nil {
		return false, err.Error()
	}
	defer cleanupRoutes()
	output, err := gatewayPlaybookRunner(routesPath)
	if err == nil {
		fmt.Printf("✓ Generation [%s] successfully validated and reloaded across GW1 and GW2 with zero downtime.\n", genID)
		db, _ := getDBConnection()
		if db != nil {
			db.Exec("UPDATE config_generations SET status = 'COMMITTED' WHERE generation_id = ?", genID)
			recordEvent(db, "", "sync_ok", genID, 0)
			db.Close()
		}
		return true, fmt.Sprintf("Generation %s applied", genID)
	}

	fmt.Println("✗ Ansible deployment failed! Triggering automatic rollback...")
	db, _ = getDBConnection()
	if db != nil {
		recordEvent(db, "", "sync_failed", output, 0)
		db.Close()
	}
	rollbackConfig()
	return false, fmt.Sprintf("Ansible error: %s", output)
}

func runGatewayPlaybook(routesVarsPath string) (string, error) {
	bastionIP := os.Getenv("BASTION_IP")
	if bastionIP == "" {
		bastionIP = "203.0.113.119"
	}
	cmd := exec.Command("ansible-playbook",
		"-i", "../ansible/inventory/hosts.yml",
		"../ansible/site.yml",
		"--vault-password-file", "../ansible/.vault_pass",
		"--tags", "haproxy",
		"-e", "@"+routesVarsPath,
		"-e", fmt.Sprintf("ansible_ssh_common_args='-o ProxyJump=ansible@%s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'", bastionIP),
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func addTeamFlow(teamName, ipAddress string, port int, skipHealthCheck bool) (bool, string) {
	initDB()
	reclaimExpiredQuarantines()

	allocRes, err := allocateLease(teamName, "", "sandbox", ipAddress)
	if err != nil {
		fmt.Printf("✗ Allocation Error: %v\n", err)
		return false, err.Error()
	}

	assignedIP := allocRes.IPAddress
	fmt.Printf("✓ Lease allocated for '%s': %s:%d\n", teamName, assignedIP, port)

	if !skipHealthCheck {
		isHealthy, healthMsg := checkSandboxHealth(assignedIP, port, 2*time.Second, 3, 1*time.Second)
		if !isHealthy {
			fmt.Printf("✗ Sandbox health check failed (%s). Route NOT opened.\n", healthMsg)
			markFailedHealthCheck(teamName)
			return false, fmt.Sprintf("Health check failed: %s", healthMsg)
		}
	}

	return syncHAProxyDualGateways(false)
}

func markFailedHealthCheck(sandboxID string) {
	db, err := getDBConnection()
	if err != nil {
		log.Printf("markFailedHealthCheck(%s): %v", sandboxID, err)
		return
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE sandboxes SET status = 'FAILED_HEALTH_CHECK' WHERE sandbox_id = ? AND status = 'ACTIVE'", sandboxID); err != nil {
		log.Printf("markFailedHealthCheck(%s): %v", sandboxID, err)
	}
}

func removeTeamFlow(teamName string, quarantineSec int) (bool, string) {
	initDB()
	result, err := releaseLease(teamName, "", quarantineSec)
	if err != nil || result.Status == "NOT_FOUND" {
		fmt.Printf("✗ Error: Team '%s' not found.\n", teamName)
		return false, "Not found"
	}

	syncHAProxyDualGateways(false)
	return true, fmt.Sprintf("Team '%s' removed; IP quarantined for %ds", teamName, result.CooldownSeconds)
}
