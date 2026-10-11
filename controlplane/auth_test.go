package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testAdminToken = "test-admin-token-1234567890"

func secureTestAPI(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", testAdminToken)
	t.Setenv("RATE_LIMIT_PER_MIN", "30")
	t.Setenv("CORS_ORIGIN", "")
	return setupAPIRoutes()
}

func TestAPIBearerAuthenticationAndOpenHealth(t *testing.T) {
	setupTestDB(t)
	handler := secureTestAPI(t)

	tests := []struct {
		name       string
		path       string
		token      string
		wantStatus int
		wantAuth   bool
	}{
		{name: "no token", path: "/api/events", wantStatus: http.StatusUnauthorized, wantAuth: true},
		{name: "wrong token", path: "/api/events", token: "wrong-token-123456789012", wantStatus: http.StatusUnauthorized, wantAuth: true},
		{name: "right token", path: "/api/events", token: testAdminToken, wantStatus: http.StatusOK},
		{name: "health is open", path: "/health", wantStatus: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.wantStatus)
			}
			if got := response.Header().Get("WWW-Authenticate"); test.wantAuth && !strings.HasPrefix(got, "Bearer ") {
				t.Fatalf("WWW-Authenticate header is missing a Bearer challenge")
			} else if !test.wantAuth && got != "" {
				t.Fatalf("unexpected WWW-Authenticate header")
			}
		})
	}
}

func TestAPIStrictJSONRejectsOversizedAndUnknownFields(t *testing.T) {
	setupTestDB(t)
	handler := secureTestAPI(t)

	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: strings.Repeat(" ", maxJSONBodySize+1)},
		{name: "unknown field", body: `{"sandbox_id":"team1","unexpected":true}`},
		{name: "multiple objects", body: `{} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/ipam/leases/allocate", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+testAdminToken)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIRateLimitAndCORSPreflight(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", testAdminToken)
	t.Setenv("RATE_LIMIT_PER_MIN", "2")
	t.Setenv("CORS_ORIGIN", "https://console.example.com")
	handler := setupAPIRoutes()

	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, "/ipam/leases/reclaim", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+testAdminToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("request %d status=%d body=%s, want 200", i+1, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/ipam/leases/reclaim", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+testAdminToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("third request status=%d body=%s, want 429", response.Code, response.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/events", nil)
	preflight.Header.Set("Origin", "https://console.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d body=%s, want 204", preflightResponse.Code, preflightResponse.Body.String())
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin=%q", got)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers=%q", got)
	}
}

func TestAPISecurityStartupRequiresToken(t *testing.T) {
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", "")
	if _, err := loadAPISecurityConfig(); err == nil {
		t.Fatal("expected missing ADMIN_TOKEN to prevent startup")
	}
}

func TestHealthReturnsUnavailableWhenDatabasePingFails(t *testing.T) {
	setupTestDB(t)
	handler := setupAPIRoutes()
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	oldDBPath := dbPath
	dbPath = filepath.Join(file, "unavailable.db")
	t.Cleanup(func() { dbPath = oldDBPath })

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want 503", response.Code, response.Body.String())
	}
}

func TestIPAndTeamValidation(t *testing.T) {
	setupTestDB(t)

	for _, body := range []string{
		`{"sandbox_id":"Team1"}`,
		`{"sandbox_id":"team1","ip_address":"127.0.0.1; touch /tmp/unsafe"}`,
	} {
		response := httptest.NewRecorder()
		handleAllocate(response, httptest.NewRequest(http.MethodPost, "/ipam/leases/allocate", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s; want 400", body, response.Code, response.Body.String())
		}
	}
}

func TestAuthMatrixAndCORS(t *testing.T) {
	setupTestDB(t)
	t.Setenv("AUTH_DISABLED", "false")
	t.Setenv("ADMIN_TOKEN", testAdminToken)
	t.Setenv("EVENTS_TOKEN", "events-token-01234567890")
	t.Setenv("CORS_ORIGIN", "https://console.example.com")
	t.Setenv("EVENTS_ALLOW_SUBNET", "false")

	handler := setupAPIRoutes()
	validEventBody := `{"type":"keepalived_state","state":"MASTER","hostname":"gw1","ts_ms":` + strconv.FormatInt(time.Now().UTC().UnixMilli(), 10) + `,"detail":"ok","sandbox_id":"team1"}`
	adminHeader := "Bearer " + testAdminToken
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		auth       string
		events     string
		remoteAddr string
		want       int
	}{
		{name: "health no auth", method: http.MethodGet, path: "/health", want: http.StatusOK},
		{name: "status no auth", method: http.MethodGet, path: "/api/gateways/status", want: http.StatusUnauthorized},
		{name: "status admin", method: http.MethodGet, path: "/api/gateways/status", auth: adminHeader, want: http.StatusOK},
		{name: "status events token", method: http.MethodGet, path: "/api/gateways/status", events: "events-token-01234567890", want: http.StatusUnauthorized},
		{name: "sync no auth", method: http.MethodPost, path: "/api/gateways/sync", body: `{}`, want: http.StatusUnauthorized},
		{name: "sync admin", method: http.MethodPost, path: "/api/gateways/sync", body: `{}`, auth: adminHeader, want: http.StatusBadGateway},
		{name: "events no auth", method: http.MethodPost, path: "/api/gateways/events", body: validEventBody, want: http.StatusUnauthorized},
		{name: "events wrong token", method: http.MethodPost, path: "/api/gateways/events", body: validEventBody, events: "bad-token", want: http.StatusUnauthorized},
		{name: "events valid token", method: http.MethodPost, path: "/api/gateways/events", body: validEventBody, events: "events-token-01234567890", want: http.StatusCreated},
		{name: "events admin bearer", method: http.MethodPost, path: "/api/gateways/events", body: validEventBody, auth: adminHeader, want: http.StatusCreated},
		{name: "sandbox no auth", method: http.MethodPost, path: "/api/sandboxes/provision", body: `{"team":"team1"}`, want: http.StatusUnauthorized},
		{name: "sandbox admin", method: http.MethodPost, path: "/api/sandboxes/provision", body: `{"team":"team1"}`, auth: adminHeader, want: http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.events != "" {
				req.Header.Set("X-Events-Token", tc.events)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("%s: got %d, want %d; body=%s", tc.name, res.Code, tc.want, res.Body.String())
			}
		})
	}

	t.Run("dashboard origin alone does not set ACAO", func(t *testing.T) {
		t.Setenv("CORS_ORIGIN", "")
		t.Setenv("DASHBOARD_ORIGIN", "https://dashboard.example.com")
		preflight := httptest.NewRequest(http.MethodOptions, "/api/gateways/status", nil)
		preflight.Header.Set("Origin", "https://dashboard.example.com")
		preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
		preflight.Header.Set("Access-Control-Request-Headers", "Authorization")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, preflight)
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("unexpected ACAO=%q with DASHBOARD_ORIGIN only", got)
		}
	})
}
