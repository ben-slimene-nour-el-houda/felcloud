package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func sendJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// setupAPIRoutes is kept as a compatibility shim for older entry points.
// The canonical server implementation is gatewayHandler() from server.go.
func setupAPIRoutes() http.Handler {
	store, err := newGatewayStore()
	if err != nil {
		log.Printf("WARNING: legacy api setup fell back to in-memory store: %v", err)
		store = &gatewayStore{db: nil}
	}
	return gatewayHandler(store)
}

func handleAllocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var params map[string]interface{}
	_ = json.Unmarshal(body, &params)
	if params == nil {
		params = make(map[string]interface{})
	}

	sandboxID, _ := params["sandbox_id"].(string)
	if sandboxID == "" {
		sandboxID, _ = params["name"].(string)
	}
	hostname, _ := params["hostname"].(string)
	preferredIP, _ := params["ip_address"].(string)
	if preferredIP == "" {
		preferredIP, _ = params["ip"].(string)
	}

	if sandboxID == "" {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing sandbox_id / name"})
		return
	}

	var ttlSec int64
	if tVal, ok := params["ttl_seconds"].(float64); ok {
		ttlSec = int64(tVal)
	} else if tVal, ok := params["ttl"].(float64); ok {
		ttlSec = int64(tVal)
	}

	res, err := allocateLeaseTTL(sandboxID, hostname, "sandbox", preferredIP, ttlSec)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds maximum allowed") {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		sendJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	sendJSON(w, http.StatusCreated, res)
}

type extendRequest struct {
	SandboxID     string `json:"sandbox_id"`
	ExtendSeconds int64  `json:"extend_seconds"`
}

func handleExtendSandbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req extendRequest
	if err := dec.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
		return
	}

	re := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	if !re.MatchString(req.SandboxID) {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid sandbox_id format"})
		return
	}

	if req.ExtendSeconds < minExtendSeconds {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("extend_seconds must be at least %d", minExtendSeconds)})
		return
	}

	db, err := getDBConnection()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}
	defer db.Close()

	newExp, err := extendSandbox(db, req.SandboxID, req.ExtendSeconds)
	if err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			sendJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Sandbox '%s' not found", req.SandboxID)})
			return
		}
		if errors.Is(err, ErrWrongState) || errors.Is(err, ErrTransitionConflict) {
			sendJSON(w, http.StatusConflict, map[string]string{"error": "Sandbox status is not ACTIVE or expires_at is null"})
			return
		}
		if errors.Is(err, ErrInvalidExtend) {
			sendJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrTTLCapExceeded) {
			sendJSON(w, 422, map[string]string{"error": "Requested extension exceeds maximum TTL cap"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to extend sandbox"})
		return
	}

	now := nowUnix()
	rem := newExp - now
	if rem < 0 {
		rem = 0
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"success":               true,
		"sandbox_id":            req.SandboxID,
		"expires_at":            newExp,
		"ttl_remaining_seconds": rem,
	})
}

func handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var params map[string]interface{}
	_ = json.Unmarshal(body, &params)
	if params == nil {
		params = make(map[string]interface{})
	}

	sandboxID, _ := params["sandbox_id"].(string)
	if sandboxID == "" {
		sandboxID, _ = params["name"].(string)
	}
	ipAddress, _ := params["ip_address"].(string)
	if ipAddress == "" {
		ipAddress, _ = params["ip"].(string)
	}

	cooldown := 0
	if c, ok := params["cooldown_seconds"].(float64); ok {
		cooldown = int(c)
	} else if c, ok := params["quarantine_seconds"].(float64); ok {
		cooldown = int(c)
	}

	if sandboxID == "" && ipAddress == "" {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing sandbox_id or ip_address"})
		return
	}

	res, err := releaseLease(sandboxID, ipAddress, cooldown)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, ErrSandboxNotFound) {
			code = http.StatusNotFound
		}
		sendJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	sendJSON(w, http.StatusOK, res)
}

func handleReclaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	count := reclaimExpiredQuarantines()
	sendJSON(w, http.StatusOK, map[string]int{"reclaimed_count": count})
}

func handleProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		provisionError(w, http.StatusMethodNotAllowed, "", "method", errors.New("method not allowed"))
		return
	}
	var req CreateReq
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		provisionError(w, http.StatusBadRequest, "", "decode", errors.New("invalid request payload"))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		provisionError(w, http.StatusBadRequest, req.Team, "decode", errors.New("request must contain one JSON object"))
		return
	}
	if req.Team == "" {
		req.Team = req.SandboxID
	}
	if req.Team == "" || !dnsLabelRE.MatchString(req.Team) {
		provisionError(w, http.StatusBadRequest, req.Team, "validate", errors.New("team must be a lowercase DNS label of 1-63 characters"))
		return
	}
	if provisioner == nil {
		provisionError(w, http.StatusBadGateway, req.Team, "config", errors.New("provisioner is not configured"))
		return
	}
	if provisionSemaphore != nil {
		select {
		case provisionSemaphore <- struct{}{}:
			defer func() { <-provisionSemaphore }()
		case <-r.Context().Done():
			provisionError(w, http.StatusGatewayTimeout, req.Team, "admission", r.Context().Err())
			return
		}
	}
	provisionSandbox(w, r, req)
}

func handleGatewaySync(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	if !failoverMu.TryLock() {
		sendJSON(w, http.StatusConflict, map[string]any{"error": "gateway operation already running", "duration_ms": int64(0)})
		return
	}
	defer failoverMu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var params map[string]interface{}
	_ = json.Unmarshal(body, &params)

	dryRun, _ := params["dry_run"].(bool)
	success, msg, duration := syncHAProxyDualGatewaysTimed(dryRun, "manual")
	code := http.StatusOK
	if !success {
		code = http.StatusBadGateway
	}
	sendJSON(w, code, map[string]interface{}{"success": success, "message": msg, "duration_ms": duration})
}

func handleGatewayRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	if !failoverMu.TryLock() {
		sendJSON(w, http.StatusConflict, map[string]any{"error": "gateway operation already running", "duration_ms": int64(0)})
		return
	}
	defer failoverMu.Unlock()
	var body struct {
		TargetGeneration string `json:"target_generation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	generation, duration, err := rollbackConfigWithOptions(body.TargetGeneration, "manual", "operator request")
	code := http.StatusOK
	if err != nil {
		code = http.StatusBadGateway
	}
	sendJSON(w, code, map[string]interface{}{"success": err == nil, "generation": generation, "duration_ms": duration, "error": errorText(err)})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runAPIServer(port int) {
	startGatewayServer(port)
}
