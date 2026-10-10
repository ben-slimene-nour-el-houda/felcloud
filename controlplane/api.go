package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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
		db, err := getDBConnection()
		if err != nil {
			sendJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "DOWN", "db": "ERROR", "timestamp": time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			sendJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "DOWN", "db": "ERROR", "timestamp": time.Now().UTC().Format(time.RFC3339),
			})
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
	mux.HandleFunc("/api/sandboxes/", handleSandboxTeardown)
	mux.HandleFunc("/api/sandboxes/extend", handleExtendSandbox)
	mux.HandleFunc("/api/reconcile", handleReconcile)
	mux.HandleFunc("/api/reconcile/apply", handleReconcileApply)
	mux.HandleFunc("/api/gateways/sync", handleGatewaySync)
	mux.HandleFunc("/api/gateways/rollback", handleGatewayRollback)

	mux.HandleFunc("/api/events", handleEvents)

	return securityHandlerOrUnavailable(mux)
}

func handleAllocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	var params struct {
		SandboxID  string `json:"sandbox_id"`
		Name       string `json:"name"`
		Hostname   string `json:"hostname"`
		IPAddress  string `json:"ip_address"`
		IP         string `json:"ip"`
		TTLSeconds *int64 `json:"ttl_seconds"`
		TTL        *int64 `json:"ttl"`
	}
	if err := decodeStrictJSON(w, r, &params); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
		return
	}

	sandboxID := params.SandboxID
	if sandboxID == "" {
		sandboxID = params.Name
	}
	hostname := params.Hostname
	preferredIP := params.IPAddress
	if preferredIP == "" {
		preferredIP = params.IP
	}

	if sandboxID == "" || !dnsLabelRE.MatchString(sandboxID) {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "sandbox_id/name must be a lowercase DNS label of 1-63 characters"})
		return
	}
	if preferredIP != "" && !validIPv4Address(preferredIP) {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "ip_address must be a literal IPv4 address"})
		return
	}

	var ttlSec int64
	if params.TTLSeconds != nil {
		ttlSec = *params.TTLSeconds
	} else if params.TTL != nil {
		ttlSec = *params.TTL
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

	var req extendRequest
	if err := decodeStrictJSON(w, r, &req); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
		return
	}

	if !dnsLabelRE.MatchString(req.SandboxID) {
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
	var params struct {
		SandboxID         string `json:"sandbox_id"`
		Name              string `json:"name"`
		IPAddress         string `json:"ip_address"`
		IP                string `json:"ip"`
		CooldownSeconds   *int   `json:"cooldown_seconds"`
		QuarantineSeconds *int   `json:"quarantine_seconds"`
	}
	if err := decodeStrictJSON(w, r, &params); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
		return
	}

	sandboxID := params.SandboxID
	if sandboxID == "" {
		sandboxID = params.Name
	}
	ipAddress := params.IPAddress
	if ipAddress == "" {
		ipAddress = params.IP
	}

	cooldown := 0
	if params.CooldownSeconds != nil {
		cooldown = *params.CooldownSeconds
	} else if params.QuarantineSeconds != nil {
		cooldown = *params.QuarantineSeconds
	}

	if sandboxID == "" && ipAddress == "" {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing sandbox_id or ip_address"})
		return
	}
	if sandboxID != "" && !dnsLabelRE.MatchString(sandboxID) {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "sandbox_id must be a lowercase DNS label"})
		return
	}
	if ipAddress != "" && !validIPv4Address(ipAddress) {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "ip_address must be a literal IPv4 address"})
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
	var request struct{}
	if err := decodeStrictJSON(w, r, &request); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
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
	if err := decodeStrictJSON(w, r, &req); err != nil {
		provisionError(w, http.StatusBadRequest, "", "decode", errors.New("invalid request payload"))
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
	var params struct {
		DryRun bool `json:"dry_run"`
	}
	if err := decodeStrictJSON(w, r, &params); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
		return
	}

	success, msg := syncHAProxyDualGateways(params.DryRun)
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
	var request struct{}
	if err := decodeStrictJSON(w, r, &request); err != nil {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request payload"})
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
	securityConfig, err := loadAPISecurityConfig()
	if err != nil {
		log.Fatalf("FATAL: API security configuration: %v", err)
	}
	logSecurityConfiguration(securityConfig)
	if err := configureProvisioner(context.Background()); err != nil {
		log.Fatalf("FATAL: provisioner configuration: %v", err)
	}
	if os.Getenv("PROVISIONER") == "mock" {
		log.Printf("!!! WARNING: PROVISIONER=mock enabled; provisioning and gateway sync results are simulated, NOT live benchmarks !!!")
	}
	if err := initDB(); err != nil {
		log.Fatalf("FATAL: initialize control-plane database: %v", err)
	}
	if report, err := reconcileReport(context.Background()); err != nil {
		log.Printf("WARNING: startup reconciliation failed: %v", err)
	} else if len(report.MissingNeutronPorts)+len(report.OrphanNeutronPorts)+len(report.IPMismatches)+len(report.MissingServers) > 0 {
		log.Printf("WARNING: startup reconciliation drift: db_without_port=%d neutron_orphans=%d ip_mismatches=%d missing_servers=%d", len(report.MissingNeutronPorts), len(report.OrphanNeutronPorts), len(report.IPMismatches), len(report.MissingServers))
	} else {
		log.Printf("startup reconciliation: no drift detected")
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
