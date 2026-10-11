package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultFailoverThreshold = 3000 * time.Millisecond

var (
	failoverMu            sync.Mutex
	failoverPollInterval  = 75 * time.Millisecond
	failoverStatusReader  = liveFailoverStatus
	failoverCommandRunner = runKeepalivedCommand
	failoverVIPProbe      = probeFailoverVIP
)

type failoverResponse struct {
	Success    bool   `json:"success"`
	Before     string `json:"before"`
	After      string `json:"after"`
	DowntimeMS int64  `json:"downtime_ms"`
	Reason     string `json:"reason,omitempty"`
}

type failoverVIPResponse struct {
	Gateway string
	Status  int
	Err     error
}

func liveFailoverStatus(store *gatewayStore) ProbeResult {
	gwCache.Invalidate()
	return gwCache.Get(store)
}

func failoverThreshold() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("FAILOVER_THRESHOLD_MS"))
	if value == "" {
		return defaultFailoverThreshold, nil
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms < 1 || ms > 60000 {
		return 0, errors.New("FAILOVER_THRESHOLD_MS must be an integer from 1 to 60000")
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func failoverProbeTarget() (*url.URL, string, error) {
	target := strings.TrimSpace(os.Getenv("FAILOVER_PROBE_URL"))
	if target == "" {
		vip := envOr("VIP_ADDRESS", "10.20.20.246")
		target = "http://" + vip + "/"
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, "", errors.New("FAILOVER_PROBE_URL must be an http(s) URL without user credentials")
	}
	return u, strings.TrimSpace(os.Getenv("FAILOVER_PROBE_HOST")), nil
}

func probeFailoverVIP(ctx context.Context, target *url.URL, host string) failoverVIPResponse {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return failoverVIPResponse{Err: err}
	}
	if host != "" {
		request.Host = host
	}
	client := &http.Client{Timeout: 300 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		return failoverVIPResponse{Err: err}
	}
	defer response.Body.Close()
	return failoverVIPResponse{Gateway: strings.TrimSpace(response.Header.Get("X-Gateway-ID")), Status: response.StatusCode}
}

func runKeepalivedCommand(gateway, action string) error {
	if !allowedGateways[gateway] || (action != "stop" && action != "start") {
		return errors.New("invalid gateway or keepalived action")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inventory := envOr("ANSIBLE_INVENTORY", "../ansible/inventory/hosts.yml")
	vault := envOr("ANSIBLE_VAULT_PASS", "../ansible/.vault_pass")
	bastion := envOr("BASTION_IP", "203.0.113.119")
	cmd := exec.CommandContext(ctx, "ansible",
		"-i", inventory, gateway,
		"-m", "command", "-a", "systemctl "+action+" keepalived", "-b",
		"--vault-password-file", vault,
		"-e", fmt.Sprintf("ansible_ssh_common_args='-o ProxyJump=ansible@%s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=3 -o BatchMode=yes'", bastion),
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Ansible %s keepalived on %s: %w (%s)", action, gateway, ctx.Err(), strings.TrimSpace(output.String()))
		}
		return fmt.Errorf("Ansible %s keepalived on %s: %w (%s)", action, gateway, err, strings.TrimSpace(output.String()))
	}
	return nil
}

func performGatewayFailover(store *gatewayStore) (result failoverResponse, statusCode int) {
	result = failoverResponse{Before: "unknown", After: "unknown"}
	statusCode = http.StatusBadGateway
	eventResult := "failed"
	stoppedGateway := ""
	startedAt := time.Now()
	var outageStarted time.Time
	defer func() {
		if stoppedGateway != "" {
			if restartErr := failoverCommandRunner(stoppedGateway, "start"); restartErr != nil {
				result.Success = false
				result.Reason = strings.TrimSpace(strings.Join([]string{result.Reason, "failed to restart keepalived: " + restartErr.Error()}, "; "))
				if statusCode != http.StatusGatewayTimeout {
					statusCode = http.StatusBadGateway
				}
				eventResult = "failed"
			} else {
				gwCache.Invalidate()
			}
		}
		if store != nil && store.db != nil {
			detail := fmt.Sprintf("from=%s to=%s result=%s", result.Before, result.After, eventResult)
			if result.Reason != "" {
				detail += " reason=" + result.Reason
			}
			recordEvent(store.db, "", "failover", detail, result.DowntimeMS)
		}
	}()

	threshold, err := failoverThreshold()
	if err != nil {
		result.Reason = err.Error()
		return result, http.StatusBadRequest
	}
	target, host, err := failoverProbeTarget()
	if err != nil {
		result.Reason = err.Error()
		return result, http.StatusBadRequest
	}
	state := failoverStatusReader(store)
	result.Before = state.VIPHolder
	result.After = state.VIPHolder
	if !allowedGateways[result.Before] || !state.Gateways[result.Before].Reachable || !state.Gateways[result.Before].VIPHolder {
		result.Reason = "live gateway status did not identify a reachable VIP holder"
		return result, http.StatusGatewayTimeout
	}

	// Arm restoration before issuing the stop command; a partial Ansible failure
	// can still leave keepalived stopped remotely.
	stoppedGateway = result.Before
	if err := failoverCommandRunner(stoppedGateway, "stop"); err != nil {
		result.Reason = "could not stop keepalived: " + err.Error()
		return result, http.StatusBadGateway
	}

	deadline := time.Now().Add(threshold)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithDeadline(context.Background(), deadline)
		probe := failoverVIPProbe(probeCtx, target, host)
		cancel()
		if probe.Err != nil || probe.Status < 200 || probe.Status >= 400 || !allowedGateways[probe.Gateway] {
			if outageStarted.IsZero() {
				outageStarted = time.Now()
			}
		} else if probe.Gateway != result.Before {
			result.After = probe.Gateway
			if !outageStarted.IsZero() {
				result.DowntimeMS = time.Since(outageStarted).Milliseconds()
			}
			result.Success = true
			result.Reason = ""
			statusCode = http.StatusOK
			eventResult = "success"
			return result, statusCode
		}
		remaining := time.Until(deadline)
		if remaining > 0 {
			wait := failoverPollInterval
			if wait > remaining {
				wait = remaining
			}
			time.Sleep(wait)
		}
	}
	if !outageStarted.IsZero() {
		result.DowntimeMS = time.Since(outageStarted).Milliseconds()
	} else {
		result.DowntimeMS = time.Since(startedAt).Milliseconds()
	}
	result.Reason = fmt.Sprintf("VIP did not move to a different gateway within %dms", threshold.Milliseconds())
	return result, http.StatusGatewayTimeout
}

func handleGatewayFailover(store *gatewayStore, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Confirm {
		errJSON(w, http.StatusBadRequest, "confirm:true required")
		return
	}
	if err := tryGatewayOp("failover-manual"); err != nil {
		errJSON(w, http.StatusConflict, err.Error())
		return
	}
	defer releaseGatewayOp("failover-manual")
	result, status := performGatewayFailover(store)
	writeJSON(w, status, result)
}
