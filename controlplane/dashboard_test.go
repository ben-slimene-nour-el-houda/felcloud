package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIConfigEndpoint(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", "felcloud-test-token-cstam-2026")
	t.Setenv("PROVISIONER", "mock")
	t.Setenv("VIP_ADDRESS", "10.20.20.246")

	handler := setupAPIRoutes()

	// 1. Must be accessible without Authorization header
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/config without auth, got %d: %s", rec.Code, rec.Body.String())
	}

	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}

	if cfg["routing_domain"] == "" {
		t.Errorf("expected non-empty routing_domain")
	}
	if cfg["vip_address"] != "10.20.20.246" {
		t.Errorf("expected vip_address=10.20.20.246, got %v", cfg["vip_address"])
	}
	if cfg["version"] != "1.0.0" {
		t.Errorf("expected version=1.0.0, got %v", cfg["version"])
	}
	if cfg["provisioner"] != "mock" {
		t.Errorf("expected provisioner=mock, got %v", cfg["provisioner"])
	}

	// 2. POST /api/config without token is rejected by auth middleware (401)
	// before the method-check handler fires. This is correct: auth first.
	postReq := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusUnauthorized && postRec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 401 (auth gate) or 405 (method check) for POST /api/config, got %d", postRec.Code)
	}
}

func TestUIDashboardHandler(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", "felcloud-test-token-cstam-2026")

	// Create temporary mock dashboard directory
	tempDir := t.TempDir()
	indexHTML := "<!DOCTYPE html><html><head><title>Dashboard</title></head><body>Root SPA</body></html>"
	if err := os.WriteFile(filepath.Join(tempDir, "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}

	assetsDir := filepath.Join(tempDir, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		t.Fatal(err)
	}
	cssContent := "body { background: #000; }"
	if err := os.WriteFile(filepath.Join(assetsDir, "index.css"), []byte(cssContent), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DASHBOARD_DIR", tempDir)
	handler := setupAPIRoutes()

	// 1. /ui redirect to /ui/
	reqRedirect := httptest.NewRequest(http.MethodGet, "/ui", nil)
	recRedirect := httptest.NewRecorder()
	handler.ServeHTTP(recRedirect, reqRedirect)
	if recRedirect.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301 for /ui, got %d", recRedirect.Code)
	}
	if loc := recRedirect.Header().Get("Location"); loc != "/ui/" {
		t.Errorf("expected redirect location /ui/, got %s", loc)
	}

	// 2. Exact file request /ui/assets/index.css
	reqCSS := httptest.NewRequest(http.MethodGet, "/ui/assets/index.css", nil)
	recCSS := httptest.NewRecorder()
	handler.ServeHTTP(recCSS, reqCSS)
	if recCSS.Code != http.StatusOK {
		t.Fatalf("expected 200 for static asset, got %d: %s", recCSS.Code, recCSS.Body.String())
	}
	if !strings.Contains(recCSS.Body.String(), "background: #000") {
		t.Errorf("unexpected css content: %s", recCSS.Body.String())
	}
	// Verify headers
	csp := recCSS.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("missing or invalid CSP header: %s", csp)
	}
	if recCSS.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing X-Content-Type-Options: nosniff")
	}

	// 3. SPA Fallback: /ui/sandboxes should return index.html
	reqSPA := httptest.NewRequest(http.MethodGet, "/ui/sandboxes", nil)
	recSPA := httptest.NewRecorder()
	handler.ServeHTTP(recSPA, reqSPA)
	if recSPA.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback path, got %d: %s", recSPA.Code, recSPA.Body.String())
	}
	if !strings.Contains(recSPA.Body.String(), "Root SPA") {
		t.Errorf("expected index.html content for SPA fallback, got %s", recSPA.Body.String())
	}

	// 4. Path traversal attempt ../ must be blocked (Go ServeMux normalises it
	//    to a 307 redirect to the clean path before our handler even fires).
	reqTraversal := httptest.NewRequest(http.MethodGet, "/ui/../secret", nil)
	recTraversal := httptest.NewRecorder()
	handler.ServeHTTP(recTraversal, reqTraversal)
	if recTraversal.Code != http.StatusNotFound && recTraversal.Code != http.StatusMovedPermanently && recTraversal.Code != http.StatusTemporaryRedirect {
		t.Errorf("expected 404 or 3xx (blocked) for path traversal attempt, got %d", recTraversal.Code)
	}
}

func TestMockGatewaysBehavior(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", "felcloud-test-token-cstam-2026")
	t.Setenv("PROVISIONER", "mock")
	t.Setenv("MOCK_GATEWAYS", "true")
	resetMockVIPHolder("gw1")

	handler := setupAPIRoutes()
	adminAuth := "Bearer felcloud-test-token-cstam-2026"

	// 1. Initial Status: gw1 should be MASTER holder, gw2 BACKUP
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/gateways/status", nil)
	reqStatus.Header.Set("Authorization", adminAuth)
	recStatus := httptest.NewRecorder()
	handler.ServeHTTP(recStatus, reqStatus)

	if recStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 from status, got %d: %s", recStatus.Code, recStatus.Body.String())
	}

	var statusResp struct {
		VIPHolder string `json:"vip_holder"`
		Gateways  map[string]struct {
			Keepalived string `json:"keepalived"`
			VIPHolder  bool   `json:"vip_holder_local"`
			Reachable  bool   `json:"reachable"`
			HAProxy    string `json:"haproxy"`
		} `json:"gateways"`
	}
	if err := json.Unmarshal(recStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("json decode status failed: %v", err)
	}

	if statusResp.VIPHolder != "gw1" {
		t.Errorf("expected initial vip_holder=gw1, got %s", statusResp.VIPHolder)
	}
	if statusResp.Gateways["gw1"].Keepalived != "MASTER" || !statusResp.Gateways["gw1"].VIPHolder {
		t.Errorf("expected gw1 to be MASTER and vip_holder_local=true")
	}
	if statusResp.Gateways["gw2"].Keepalived != "BACKUP" || statusResp.Gateways["gw2"].VIPHolder {
		t.Errorf("expected gw2 to be BACKUP and vip_holder_local=false")
	}

	// 2. Perform Failover: flips holder in memory after ~800ms
	failoverBody := `{"confirm":true,"target_gateway":"gw2","mode":"keepalived"}`
	start := time.Now()
	reqFailover := httptest.NewRequest(http.MethodPost, "/api/gateways/failover", strings.NewReader(failoverBody))
	reqFailover.Header.Set("Authorization", adminAuth)
	reqFailover.Header.Set("Content-Type", "application/json")
	recFailover := httptest.NewRecorder()
	handler.ServeHTTP(recFailover, reqFailover)

	elapsed := time.Since(start)
	if recFailover.Code != http.StatusOK {
		t.Fatalf("expected 200 from failover, got %d: %s", recFailover.Code, recFailover.Body.String())
	}
	if elapsed < 750*time.Millisecond {
		t.Errorf("expected failover to simulate realistic ~800ms delay, took %v", elapsed)
	}

	var failoverResp struct {
		Success bool   `json:"success"`
		Before  string `json:"before"`
		After   string `json:"after"`
	}
	if err := json.Unmarshal(recFailover.Body.Bytes(), &failoverResp); err != nil {
		t.Fatalf("json decode failover failed: %v", err)
	}
	if !failoverResp.Success || failoverResp.Before != "gw1" || failoverResp.After != "gw2" {
		t.Errorf("unexpected failover response: %+v", failoverResp)
	}

	// 3. Verify status after failover: gw2 is now MASTER
	recStatus2 := httptest.NewRecorder()
	handler.ServeHTTP(recStatus2, reqStatus)
	var statusResp2 struct {
		VIPHolder string `json:"vip_holder"`
		Gateways  map[string]struct {
			Keepalived string `json:"keepalived"`
			VIPHolder  bool   `json:"vip_holder_local"`
		} `json:"gateways"`
	}
	_ = json.Unmarshal(recStatus2.Body.Bytes(), &statusResp2)
	if statusResp2.VIPHolder != "gw2" {
		t.Errorf("expected post-failover vip_holder=gw2, got %s", statusResp2.VIPHolder)
	}
	if statusResp2.Gateways["gw2"].Keepalived != "MASTER" || !statusResp2.Gateways["gw2"].VIPHolder {
		t.Errorf("expected gw2 to be MASTER post-failover")
	}

	// 4. Test Mock Gateway Logs (returns sample lines with 5xx)
	reqLogs := httptest.NewRequest(http.MethodGet, "/api/gateways/logs?gateway=gw1&source=haproxy&limit=50", nil)
	reqLogs.Header.Set("Authorization", adminAuth)
	recLogs := httptest.NewRecorder()
	handler.ServeHTTP(recLogs, reqLogs)

	if recLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 from logs, got %d: %s", recLogs.Code, recLogs.Body.String())
	}

	var logsResp struct {
		Count   int      `json:"count"`
		Entries []string `json:"entries"`
	}
	if err := json.Unmarshal(recLogs.Body.Bytes(), &logsResp); err != nil {
		t.Fatalf("json decode logs failed: %v", err)
	}
	if logsResp.Count == 0 || len(logsResp.Entries) == 0 {
		t.Errorf("expected mock log entries, got 0")
	}

	has5xx := false
	for _, l := range logsResp.Entries {
		if strings.Contains(l, " 502 ") || strings.Contains(l, " 504 ") || strings.Contains(l, " 503 ") {
			has5xx = true
			break
		}
	}
	if !has5xx {
		t.Errorf("expected mock logs to include sample 5xx lines for UI highlighting verification")
	}
}
