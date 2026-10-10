package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ── Error envelope ─────────────────────────────────────────────────────────────

type apiErr struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
	Code   int    `json:"code"`
}

func errJSON(w http.ResponseWriter, status int, msg string, detail ...string) {
	e := apiErr{Error: msg, Code: status}
	if len(detail) > 0 {
		e.Detail = detail[0]
	}
	writeJSON(w, status, e)
}

// ── DB store ───────────────────────────────────────────────────────────────────

type gatewayStore struct {
	db *sql.DB
}

func newGatewayStore() (*gatewayStore, error) {
	p := filepath.Join("..", "ipam", "ipam.db")
	db, err := sql.Open("sqlite3", p+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(30 * time.Second)
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &gatewayStore{db: db}, nil
}

func (s *gatewayStore) close() { _ = s.db.Close() }

// ── Auth middleware ────────────────────────────────────────────────────────────

// requireBearerToken enforces CONTROL_PLANE_TOKEN when set.
func requireBearerToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight — always allow
		if r.Method == http.MethodOptions {
			writeJSON(w, http.StatusNoContent, nil)
			return
		}
		token := os.Getenv("CONTROL_PLANE_TOKEN")
		if token == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
			errJSON(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		next(w, r)
	}
}

// requireEventsAuth is the dual-token check for POST /api/gateways/events.
// Priority: Bearer CONTROL_PLANE_TOKEN → X-Events-Token → gateway mgmt subnet (only when both env vars are unset).
func requireEventsAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			writeJSON(w, http.StatusNoContent, nil)
			return
		}
		cpToken := os.Getenv("CONTROL_PLANE_TOKEN")
		evToken := os.Getenv("EVENTS_TOKEN")

		// 1. Bearer admin token (dashboard / operators)
		if cpToken != "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") && strings.TrimPrefix(auth, "Bearer ") == cpToken {
				next(w, r)
				return
			}
			// token is configured but not matched → reject; don't fall through
			errJSON(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		// 2. X-Events-Token for notify scripts (lightweight shared secret)
		if evToken != "" {
			if r.Header.Get("X-Events-Token") == evToken {
				next(w, r)
				return
			}
			// token is configured but not matched → reject; don't fall through
			errJSON(w, http.StatusUnauthorized, "invalid X-Events-Token")
			return
		}
		// 3. Neither token env var is set: accept only from the gateway mgmt subnet.
		// Gateway mgmt NICs: gw1=10.20.10.24, gw2=10.20.10.164 (subnet 10.20.10.0/24).
		// Confirmed in Pulumi.dev.yaml (felcloud-infra:mgmtCidr), Ansible inventory,
		// and IPAM DB subnets table. Uses r.RemoteAddr only — never X-Forwarded-For.
		gwSubnet := envOr("GATEWAY_SUBNET", "10.20.10.0/24")
		if fromRemoteAddr(r, gwSubnet) {
			next(w, r)
			return
		}
		errJSON(w, http.StatusForbidden, "forbidden: not from gateway subnet")
	}
}

// fromRemoteAddr checks whether r.RemoteAddr belongs to cidr.
// Uses ONLY r.RemoteAddr — X-Forwarded-For is intentionally ignored (trivially spoofed).
func fromRemoteAddr(r *http.Request, cidr string) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without port (unusual but possible in tests)
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return network.Contains(ip)
}

// ── JSON / CORS helpers ───────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, payload any) {
	origin := strings.TrimSpace(os.Getenv("DASHBOARD_ORIGIN"))
	w.Header().Set("Content-Type", "application/json")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Events-Token")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func logRequest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		log.Printf("gw-api %s %s %s %.1fms", r.Method, r.URL.Path, r.RemoteAddr, float64(time.Since(start).Microseconds())/1000)
	}
}

// ── Allowed value sets ─────────────────────────────────────────────────────────

var allowedGateways = map[string]bool{"gw1": true, "gw2": true}
var allowedSources = map[string]bool{"haproxy": true, "keepalived": true}
var allowedEventTypes = map[string]bool{
	"failover":         true,
	"rollback":         true,
	"keepalived_state": true,
	"sync":             true,
	"failover_test":    true,
	"health_check":     true,
}

// ── Router ─────────────────────────────────────────────────────────────────────

func gatewayHandler(store *gatewayStore) http.Handler {
	mux := http.NewServeMux()

	// ── Health ─────────────────────────────────────────────────────────────────
	mux.HandleFunc("/health", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "UP",
			"source":    "controlplane-gateway",
			"db":        "OK",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}))

	// ── GET /api/gateways/status ───────────────────────────────────────────────
	mux.HandleFunc("/api/gateways/status", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1" {
			gwCache.Invalidate()
		}
		pr := gwCache.Get(store)

		// Build per-gateway map with the VIP address included
		vip := envOr("VIP_ADDRESS", "10.20.20.246")
		gwOut := make(map[string]any, len(pr.Gateways))
		for name, p := range pr.Gateways {
			gwOut[name] = map[string]any{
				"reachable":        p.Reachable,
				"haproxy":          p.HAProxy,
				"keepalived":       p.Keepalived,
				"vip_holder_local": p.VIPHolder,
				"probed_at":        p.ProbedAt,
				"error":            p.Error,
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"gateways":          gwOut,
			"vip":               vip,
			"vip_holder":        pr.VIPHolder,
			"latest_generation": pr.LatestGenID,
			"fetched_at_ms":     pr.FetchedAtUnix,
		})
	})))

	// ── GET /api/gateways/logs ─────────────────────────────────────────────────
	mux.HandleFunc("/api/gateways/logs", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		q := r.URL.Query()
		gateway := q.Get("gateway")
		source := q.Get("source")
		if source == "" {
			source = "haproxy"
		}
		since := q.Get("since")

		if gateway == "" {
			errJSON(w, http.StatusBadRequest, "gateway param required", "valid values: gw1, gw2")
			return
		}
		if !allowedGateways[gateway] {
			errJSON(w, http.StatusBadRequest, "invalid gateway", "valid values: gw1, gw2")
			return
		}
		if !allowedSources[source] {
			errJSON(w, http.StatusBadRequest, "invalid source", "valid values: haproxy, keepalived")
			return
		}
		limit := 50
		if ls := q.Get("limit"); ls != "" {
			if l, err := strconv.Atoi(ls); err == nil {
				if l < 1 {
					limit = 1
				} else if l > 500 {
					limit = 500
				} else {
					limit = l
				}
			}
		}

		lines, err := FetchGatewayLogs(gateway, source, since, limit)
		if err != nil {
			errStr := err.Error()
			code := http.StatusBadGateway // 502 = ansible/ssh error
			if strings.Contains(errStr, "timed out") {
				code = http.StatusGatewayTimeout // 504 = timeout specifically
			}
			// Include applied_limit so callers can confirm clamping even in error path
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":         "log fetch failed",
				"detail":        errStr,
				"code":          code,
				"applied_limit": limit,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gateway": gateway,
			"source":  source,
			"since":   since,
			"limit":   limit,
			"count":   len(lines),
			"entries": lines,
		})
	})))

	// ── GET /api/gateways/events   POST /api/gateways/events ──────────────────
	mux.HandleFunc("/api/gateways/events", logRequest(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodOptions:
			writeJSON(w, http.StatusNoContent, nil)

		case http.MethodGet:
			requireBearerToken(func(w http.ResponseWriter, r *http.Request) {
				gwEventsGet(w, r, store)
			})(w, r)

		case http.MethodPost:
			requireEventsAuth(func(w http.ResponseWriter, r *http.Request) {
				gwEventsPost(w, r, store)
			})(w, r)

		default:
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))

	// ── GET /api/gateways/generations ─────────────────────────────────────────
	mux.HandleFunc("/api/gateways/generations", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		gwGenerationsGet(w, r, store)
	})))

	// ── POST /api/gateways/failover ────────────────────────────────────────────
	mux.HandleFunc("/api/gateways/failover", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		handleGatewayFailover(store, w, r)
	})))

	// ── POST /api/gateways/sync ────────────────────────────────────────────────
	mux.HandleFunc("/api/gateways/sync", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !failoverMu.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "gateway operation already running", "duration_ms": int64(0)})
			return
		}
		defer failoverMu.Unlock()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			errJSON(w, http.StatusBadRequest, "invalid JSON", err.Error())
			return
		}
		dryRun, _ := body["dry_run"].(bool)
		success, msg, duration := syncHAProxyDualGatewaysTimed(dryRun, "manual")
		if !success {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "sync failed", "reason": msg, "duration_ms": duration})
			return
		}
		gwCache.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg, "duration_ms": duration})
	})))

	// ── POST /api/gateways/rollback ────────────────────────────────────────────
	mux.HandleFunc("/api/gateways/rollback", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !failoverMu.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "gateway operation already running", "duration_ms": int64(0)})
			return
		}
		defer failoverMu.Unlock()
		var body struct {
			TargetGeneration string `json:"target_generation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			errJSON(w, http.StatusBadRequest, "invalid JSON", err.Error())
			return
		}
		generation, duration, err := rollbackConfigWithOptions(body.TargetGeneration, "manual", "operator request")
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "rollback failed", "reason": err.Error(), "duration_ms": duration})
			return
		}
		gwCache.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "generation": generation, "duration_ms": duration})
	})))

	// ── GET /api/sandboxes ─────────────────────────────────────────────────────
	mux.HandleFunc("/api/sandboxes", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		gwSandboxesGet(w, r)
	})))

	// ── GET /api/leases + /ipam/leases ────────────────────────────────────────
	leasesHandler := requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		gwLeasesGet(w, r)
	}))
	mux.HandleFunc("/api/leases", leasesHandler)
	mux.HandleFunc("/ipam/leases", leasesHandler)

	// ── Catch-all ──────────────────────────────────────────────────────────────
	mux.HandleFunc("/", logRequest(func(w http.ResponseWriter, r *http.Request) {
		errJSON(w, http.StatusNotFound, "not found", r.URL.Path)
	}))

	return mux
}

// ── /api/gateways/events GET ──────────────────────────────────────────────────

func gwEventsGet(w http.ResponseWriter, r *http.Request, _ *gatewayStore) {
	q := r.URL.Query()

	limit := 50
	if ls := q.Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil {
			switch {
			case l < 1:
				limit = 1
			case l > 500:
				limit = 500
			default:
				limit = l
			}
		}
	}

	typFilter := q.Get("type")
	sinceFilter := q.Get("since") // epoch ms

	db, err := getDBConnection()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "db error", err.Error())
		return
	}
	defer db.Close()

	query := `SELECT id, ts_ms, COALESCE(sandbox_id,''), type, COALESCE(detail,''), COALESCE(duration_ms,0)
	          FROM events WHERE 1=1`
	args := []any{}

	if typFilter != "" {
		query += " AND type = ?"
		args = append(args, typFilter)
	}
	if sinceFilter != "" {
		if ms, err := strconv.ParseInt(sinceFilter, 10, 64); err == nil {
			query += " AND ts_ms >= ?"
			args = append(args, ms)
		}
	}
	query += " ORDER BY ts_ms DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "query error", err.Error())
		return
	}
	defer rows.Close()

	var events []map[string]any
	for rows.Next() {
		var id int
		var tsMs, durMs int64
		var sandboxID, typ, detail string
		if err := rows.Scan(&id, &tsMs, &sandboxID, &typ, &detail, &durMs); err == nil {
			events = append(events, map[string]any{
				"id":          id,
				"ts_ms":       tsMs,
				"ts_iso":      time.UnixMilli(tsMs).UTC().Format(time.RFC3339),
				"sandbox_id":  sandboxID,
				"type":        typ,
				"detail":      detail,
				"duration_ms": durMs,
			})
		}
	}
	if events == nil {
		events = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "count": len(events), "limit": limit})
}

// ── /api/gateways/events POST ─────────────────────────────────────────────────

type eventsPostBody struct {
	Type       string `json:"type"`
	State      string `json:"state"`
	Host       string `json:"hostname"`
	TsMs       int64  `json:"ts_ms"`
	DurationMs int64  `json:"duration_ms"`
	Detail     string `json:"detail"`
	SandboxID  string `json:"sandbox_id"`
}

func gwEventsPost(w http.ResponseWriter, r *http.Request, _ *gatewayStore) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var body eventsPostBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errJSON(w, http.StatusBadRequest, "invalid JSON body", err.Error())
		return
	}
	if body.Type == "" {
		errJSON(w, http.StatusBadRequest, "type is required")
		return
	}
	if !allowedEventTypes[body.Type] {
		types := make([]string, 0, len(allowedEventTypes))
		for t := range allowedEventTypes {
			types = append(types, t)
		}
		errJSON(w, http.StatusBadRequest, "invalid type",
			fmt.Sprintf("allowed: %s", strings.Join(types, ", ")))
		return
	}

	if body.TsMs == 0 {
		body.TsMs = time.Now().UTC().UnixMilli()
	}

	// Build detail string from fields if not explicitly set
	detail := body.Detail
	if detail == "" && (body.State != "" || body.Host != "") {
		detail = fmt.Sprintf("state=%s host=%s", body.State, body.Host)
	}

	db, err := getDBConnection()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "db error", err.Error())
		return
	}
	defer db.Close()

	recordEvent(db, body.SandboxID, body.Type, detail, body.DurationMs)

	writeJSON(w, http.StatusCreated, map[string]any{
		"accepted":   true,
		"type":       body.Type,
		"ts_ms":      body.TsMs,
		"detail":     detail,
		"sandbox_id": body.SandboxID,
	})
}

// ── /api/gateways/generations GET ─────────────────────────────────────────────

func gwGenerationsGet(w http.ResponseWriter, r *http.Request, _ *gatewayStore) {
	q := r.URL.Query()
	limit := 20
	if ls := q.Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	db, err := getDBConnection()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "db error", err.Error())
		return
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, generation_id, status, created_at, config_snapshot, COALESCE(gw1_backup,''), COALESCE(gw2_backup,'')
		FROM config_generations
		ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "query error", err.Error())
		return
	}
	defer rows.Close()

	var gens []map[string]any
	for rows.Next() {
		var id int
		var genID, status, createdAt, snapshot, gw1Backup, gw2Backup string
		if err := rows.Scan(&id, &genID, &status, &createdAt, &snapshot, &gw1Backup, &gw2Backup); err == nil {
			// Count backend stanzas as a proxy for "number of active routes"
			backendCount := strings.Count(snapshot, "\nbackend ")
			gens = append(gens, map[string]any{
				"id":            id,
				"generation_id": genID,
				"status":        status,
				"created_at":    createdAt,
				"backend_count": backendCount,
				"gw1_backup":    gw1Backup,
				"gw2_backup":    gw2Backup,
			})
		}
	}
	if gens == nil {
		gens = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"generations": gens, "count": len(gens)})
}

// ── /api/sandboxes GET ────────────────────────────────────────────────────────

func gwSandboxesGet(w http.ResponseWriter, _ *http.Request) {
	db, err := getDBConnection()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "db error", err.Error())
		return
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, sandbox_id, hostname, subnet_name,
		       COALESCE(private_ip,'') AS private_ip,
		       status, created_at, updated_at, expires_at
		FROM sandboxes ORDER BY created_at`)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "query error", err.Error())
		return
	}
	defer rows.Close()

	var sbs []map[string]any
	for rows.Next() {
		var id int
		var sandboxID, hostname, subnet, ip, status, createdAt, updatedAt string
		var expiresAt sql.NullInt64
		if err := rows.Scan(&id, &sandboxID, &hostname, &subnet, &ip, &status, &createdAt, &updatedAt, &expiresAt); err == nil {
			m := map[string]any{
				"id":          id,
				"sandbox_id":  sandboxID,
				"hostname":    hostname,
				"subnet_name": subnet,
				"private_ip":  ip,
				"status":      status,
				"created_at":  createdAt,
				"updated_at":  updatedAt,
			}
			if expiresAt.Valid {
				now := time.Now().UTC().Unix()
				rem := expiresAt.Int64 - now
				if rem < 0 {
					rem = 0
				}
				m["expires_at"] = expiresAt.Int64
				m["ttl_remaining_seconds"] = rem
			} else {
				m["expires_at"] = nil
				m["ttl_remaining_seconds"] = nil
			}
			sbs = append(sbs, m)
		}
	}
	if sbs == nil {
		sbs = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sandboxes": sbs, "count": len(sbs)})
}

// ── /api/leases GET ───────────────────────────────────────────────────────────

func gwLeasesGet(w http.ResponseWriter, _ *http.Request) {
	db, err := getDBConnection()
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "db error", err.Error())
		return
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, ip_address, subnet_name, entity_name, entity_type, status,
		       COALESCE(quarantined_at,'')    AS quarantined_at,
		       COALESCE(quarantine_until,'')  AS quarantine_until,
		       COALESCE(assigned_at,'')       AS assigned_at
		FROM ip_allocations ORDER BY subnet_name, ip_address`)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, "query error", err.Error())
		return
	}
	defer rows.Close()

	var leases []map[string]any
	for rows.Next() {
		var id int
		var ip, subnet, entity, eType, status, qAt, qUntil, aAt string
		if err := rows.Scan(&id, &ip, &subnet, &entity, &eType, &status, &qAt, &qUntil, &aAt); err == nil {
			leases = append(leases, map[string]any{
				"id":               id,
				"ip_address":       ip,
				"subnet_name":      subnet,
				"entity_name":      entity,
				"entity_type":      eType,
				"status":           status,
				"quarantined_at":   qAt,
				"quarantine_until": qUntil,
				"assigned_at":      aAt,
			})
		}
	}
	if leases == nil {
		leases = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": leases, "count": len(leases)})
}

// ── HTTP server entry point ────────────────────────────────────────────────────

// startGatewayServer starts the gateway HTTP API on the given port.
// Called by main.go's "serve" subcommand.
func startGatewayServer(port int) {
	if err := initDB(); err != nil {
		log.Printf("WARNING: initDB: %v (continuing)", err)
	}

	store, err := newGatewayStore()
	if err != nil {
		log.Printf("WARNING: gateway sqlite store: %v (status/generations endpoints degraded)", err)
		store = &gatewayStore{db: func() *sql.DB {
			db, _ := sql.Open("sqlite3", ":memory:")
			return db
		}()}
	}
	defer store.close()

	addr := fmt.Sprintf(":%d", port)
	log.Printf("control-plane gateway API listening on %s", addr)
	if err := http.ListenAndServe(addr, gatewayHandler(store)); err != nil {
		log.Fatal(err)
	}
}
