package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func failoverTestStore(t *testing.T) *gatewayStore {
	t.Helper()
	setupTestDB(t)
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	return &gatewayStore{db: db}
}

func setFailoverTestHooks(t *testing.T, status ProbeResult, command func(string, string) error, probe func(*url.URL, string) failoverVIPResponse) {
	t.Helper()
	oldStatus, oldCommand, oldProbe, oldInterval := failoverStatusReader, failoverCommandRunner, failoverVIPProbe, failoverPollInterval
	failoverStatusReader = func(*gatewayStore) ProbeResult { return status }
	failoverCommandRunner = command
	failoverVIPProbe = func(_ context.Context, target *url.URL, host string) failoverVIPResponse { return probe(target, host) }
	failoverPollInterval = time.Millisecond
	t.Cleanup(func() {
		failoverStatusReader, failoverCommandRunner, failoverVIPProbe, failoverPollInterval = oldStatus, oldCommand, oldProbe, oldInterval
	})
}

func TestGatewayFailoverStopsRestartsAndRecordsRealTransition(t *testing.T) {
	store := failoverTestStore(t)
	defer store.close()
	t.Setenv("CONTROL_PLANE_TOKEN", "")
	t.Setenv("FAILOVER_THRESHOLD_MS", "200")
	t.Setenv("FAILOVER_PROBE_URL", "http://192.0.2.10/health")
	status := ProbeResult{VIPHolder: "gw1", Gateways: map[string]GWProbe{
		"gw1": {Reachable: true, VIPHolder: true}, "gw2": {Reachable: true},
	}}
	var mu sync.Mutex
	var commands []string
	setFailoverTestHooks(t, status, func(host, action string) error {
		mu.Lock()
		commands = append(commands, host+":"+action)
		mu.Unlock()
		return nil
	}, func(*url.URL, string) failoverVIPResponse {
		return failoverVIPResponse{Gateway: "gw2", Status: http.StatusOK}
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/gateways/failover", strings.NewReader(`{"confirm":true}`))
	handleGatewayFailover(store, recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"before":"gw1"`) || !strings.Contains(recorder.Body.String(), `"after":"gw2"`) {
		t.Fatalf("response did not report observed transition: %s", recorder.Body.String())
	}
	mu.Lock()
	if strings.Join(commands, ",") != "gw1:stop,gw1:start" {
		t.Fatalf("commands=%v", commands)
	}
	mu.Unlock()
	var detail string
	var duration int64
	if err := store.db.QueryRow(`SELECT detail,duration_ms FROM events WHERE type='failover' ORDER BY id DESC LIMIT 1`).Scan(&detail, &duration); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "from=gw1 to=gw2 result=success") || duration < 0 {
		t.Fatalf("event detail=%q duration=%d", detail, duration)
	}
}

func TestGatewayFailoverTimeoutRestartsAndReturnsReason(t *testing.T) {
	store := failoverTestStore(t)
	defer store.close()
	t.Setenv("CONTROL_PLANE_TOKEN", "")
	t.Setenv("FAILOVER_THRESHOLD_MS", "10")
	t.Setenv("FAILOVER_PROBE_URL", "http://192.0.2.10/health")
	status := ProbeResult{VIPHolder: "gw1", Gateways: map[string]GWProbe{"gw1": {Reachable: true, VIPHolder: true}}}
	var mu sync.Mutex
	var commands []string
	setFailoverTestHooks(t, status, func(host, action string) error {
		mu.Lock()
		commands = append(commands, host+":"+action)
		mu.Unlock()
		return nil
	}, func(*url.URL, string) failoverVIPResponse {
		return failoverVIPResponse{Gateway: "gw1", Status: http.StatusOK}
	})
	recorder := httptest.NewRecorder()
	handleGatewayFailover(store, recorder, httptest.NewRequest(http.MethodPost, "/api/gateways/failover", strings.NewReader(`{"confirm":true}`)))
	if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), "VIP did not move") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(commands, ",") != "gw1:stop,gw1:start" {
		t.Fatalf("keepalived was not restored after timeout: %v", commands)
	}
}

func TestGatewayFailoverConcurrentRequestGetsConflict(t *testing.T) {
	store := failoverTestStore(t)
	defer store.close()
	if !failoverMu.TryLock() {
		t.Fatal("test could not acquire failover mutex")
	}
	recorder := httptest.NewRecorder()
	handleGatewayFailover(store, recorder, httptest.NewRequest(http.MethodPost, "/api/gateways/failover", strings.NewReader(`{"confirm":true}`)))
	failoverMu.Unlock()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayFailoverRestartsAfterStopCommandError(t *testing.T) {
	store := failoverTestStore(t)
	defer store.close()
	t.Setenv("CONTROL_PLANE_TOKEN", "")
	t.Setenv("FAILOVER_THRESHOLD_MS", "20")
	t.Setenv("FAILOVER_PROBE_URL", "http://192.0.2.10/health")
	status := ProbeResult{VIPHolder: "gw2", Gateways: map[string]GWProbe{"gw2": {Reachable: true, VIPHolder: true}}}
	var mu sync.Mutex
	var commands []string
	setFailoverTestHooks(t, status, func(host, action string) error {
		mu.Lock()
		commands = append(commands, host+":"+action)
		mu.Unlock()
		if action == "stop" {
			return errors.New("partial remote failure")
		}
		return nil
	}, func(*url.URL, string) failoverVIPResponse { return failoverVIPResponse{} })
	recorder := httptest.NewRecorder()
	handleGatewayFailover(store, recorder, httptest.NewRequest(http.MethodPost, "/api/gateways/failover", strings.NewReader(`{"confirm":true}`)))
	mu.Lock()
	defer mu.Unlock()
	if recorder.Code != http.StatusBadGateway || strings.Join(commands, ",") != "gw2:stop,gw2:start" {
		t.Fatalf("status=%d commands=%v body=%s", recorder.Code, commands, recorder.Body.String())
	}
}
