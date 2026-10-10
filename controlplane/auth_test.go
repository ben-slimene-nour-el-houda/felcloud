package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
