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

func setupAPIRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
			return
		}
		sendJSON(w, http.StatusOK, map[string]string{
			"status":    "UP",
			"version":   "2.0",
			"db":        "OK",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/ipam/leases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
			return
		}
		reclaimExpiredQuarantines()
		db, err := getDBConnection()
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer db.Close()

		rows, err := db.Query("SELECT id, ip_address, subnet_name, entity_name, entity_type, status, COALESCE(quarantined_at, '') as quarantined_at, COALESCE(quarantine_until, '') as quarantine_until, COALESCE(assigned_at, '') as assigned_at FROM ip_allocations ORDER BY subnet_name, ip_address")
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		var leases []map[string]interface{}
		for rows.Next() {
			var id int
			var ip, sub, entity, eType, st, qAt, qUntil, aAt string
			if err := rows.Scan(&id, &ip, &sub, &entity, &eType, &st, &qAt, &qUntil, &aAt); err == nil {
				leases = append(leases, map[string]interface{}{
					"id":               id,
					"ip_address":       ip,
					"subnet_name":      sub,
					"entity_name":      entity,
					"entity_type":      eType,
					"status":           st,
					"quarantined_at":   qAt,
					"quarantine_until": qUntil,
					"assigned_at":      aAt,
				})
			}
		}
		sendJSON(w, http.StatusOK, map[string]interface{}{"leases": leases})
	})
	mux.HandleFunc("/api/leases", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ipam/leases", http.StatusTemporaryRedirect)
	})

	mux.HandleFunc("/api/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
			return
		}
		db, err := getDBConnection()
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer db.Close()

		rows, err := db.Query("SELECT id, sandbox_id, hostname, subnet_name, COALESCE(private_ip, '') as private_ip, status, created_at, updated_at, expires_at FROM sandboxes ORDER BY created_at")
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		var sbs []map[string]interface{}
		for rows.Next() {
			var id int
			var sID, host, sub, ip, st, cAt, uAt string
			var expVal sql.NullInt64
			if err := rows.Scan(&id, &sID, &host, &sub, &ip, &st, &cAt, &uAt, &expVal); err == nil {
				sbMap := map[string]interface{}{
					"id":          id,
					"sandbox_id":  sID,
					"hostname":    host,
					"subnet_name": sub,
					"private_ip":  ip,
					"status":      st,
					"created_at":  cAt,
					"updated_at":  uAt,
				}
				if expVal.Valid {
					now := nowUnix()
					rem := expVal.Int64 - now
					if rem < 0 {
						rem = 0
					}
					sbMap["expires_at"] = expVal.Int64
					sbMap["ttl_remaining_seconds"] = rem
				} else {
					sbMap["expires_at"] = nil
					sbMap["ttl_remaining_seconds"] = nil
				}
				sbs = append(sbs, sbMap)
			}
		}
		sendJSON(w, http.StatusOK, map[string]interface{}{"sandboxes": sbs})
	})

	mux.HandleFunc("/ipam/leases/allocate", handleAllocate)
	mux.HandleFunc("/api/leases/allocate", handleAllocate)

	mux.HandleFunc("/ipam/leases/release", handleRelease)
	mux.HandleFunc("/api/leases/release", handleRelease)

	mux.HandleFunc("/ipam/leases/reclaim", handleReclaim)
	mux.HandleFunc("/api/leases/reclaim", handleReclaim)

	mux.HandleFunc("/api/sandboxes/provision", handleProvision)
	mux.HandleFunc("/api/sandboxes/extend", handleExtendSandbox)
	mux.HandleFunc("/api/gateways/sync", handleGatewaySync)
	mux.HandleFunc("/api/gateways/rollback", handleGatewayRollback)

	mux.HandleFunc("/api/events", handleEvents)

	return mux
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
	body, _ := io.ReadAll(r.Body)
	var params map[string]interface{}
	_ = json.Unmarshal(body, &params)

	dryRun, _ := params["dry_run"].(bool)
	success, msg := syncHAProxyDualGateways(dryRun)
	code := http.StatusOK
	if !success {
		code = http.StatusInternalServerError
	}
	sendJSON(w, code, map[string]interface{}{"success": success, "message": msg})
}

func handleGatewayRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	success := rollbackConfig()
	code := http.StatusOK
	if !success {
		code = http.StatusInternalServerError
	}
	sendJSON(w, code, map[string]bool{"success": success})
}
func runAPIServer(port int) {
	if err := configureProvisioner(context.Background()); err != nil {
		log.Fatalf("FATAL: provisioner configuration: %v", err)
	}
	if err := initDB(); err != nil {
		log.Fatalf("FATAL: initialize control-plane database: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = startReaper(ctx)

	host := os.Getenv("LISTEN_ADDR")
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	server := &http.Server{
		Addr:    addr,
		Handler: setupAPIRoutes(),
	}
	fmt.Printf("✓ FelCloud IPAM & Gateway Control Plane API (Go) running on http://%s\n", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Error: %v\n", err)
	}
}
