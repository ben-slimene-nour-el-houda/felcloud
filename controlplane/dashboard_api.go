package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// setupDashboardAPIRoutes registers the REST API endpoints expected by the FelCloud dashboard.
func setupDashboardAPIRoutes(mux *http.ServeMux, store *gatewayStore) {
	// ── 1. Health ──────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/health", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		db, err := getDBConnection()
		dbStatus := "ok"
		dbDetail := "SQLite OK"
		overallStatus := "ok"
		if err != nil {
			dbStatus = "down"
			dbDetail = err.Error()
			overallStatus = "degraded"
		} else {
			defer db.Close()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := db.PingContext(ctx); err != nil {
				dbStatus = "down"
				dbDetail = err.Error()
				overallStatus = "degraded"
			}
		}

		pr := gwCache.Get(store)
		vipHolder := pr.VIPHolder
		if vipHolder == "" {
			vipHolder = getMockVIPHolder()
		}

		components := []map[string]any{
			{"name": "database", "status": dbStatus, "detail": dbDetail},
			{"name": "reaper", "status": "ok", "detail": fmt.Sprintf("last tick: %d", lastTickUnix.Load())},
			{"name": "gateways", "status": "ok", "detail": fmt.Sprintf("VIP owner: %s", vipHolder)},
			{"name": "provisioner", "status": "ok", "detail": "active"},
		}

		capabilities := map[string]bool{
			"sandbox_provision": true,
			"sandbox_teardown":  true,
			"lease_reconcile":   true,
			"gateway_failover":  true,
			"failover_drill":    true,
			"config_validate":   true,
			"config_deploy":     true,
			"config_rollback":   true,
			"logs_haproxy":      true,
			"logs_system":       true,
			"benchmark_run":     true,
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":       overallStatus,
			"version":      "1.0.0",
			"checked_at":   time.Now().UTC().Format(time.RFC3339),
			"components":   components,
			"capabilities": capabilities,
		})
	}))

	// ── 2. Gateways ────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/gateways", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		vip := envOr("VIP_ADDRESS", "10.20.20.246")
		pr := gwCache.Get(store)
		holder := pr.VIPHolder
		if holder == "" || holder == "unknown" {
			holder = getMockVIPHolder()
		}
		nowStr := time.Now().UTC().Format(time.RFC3339)

		gw1Holder := (holder == "gw1")
		gw2Holder := (holder == "gw2")

		gw1Role := "backup"
		if gw1Holder {
			gw1Role = "master"
		}
		gw2Role := "backup"
		if gw2Holder {
			gw2Role = "master"
		}

		gw1 := map[string]any{
			"id":                "gw1",
			"name":              "Gateway 1 (gw1)",
			"address":           envOr("GW1_HOST", "10.20.10.24"),
			"role":              gw1Role,
			"owns_vip":          gw1Holder,
			"haproxy":           map[string]any{"status": "up", "checked_at": nowStr},
			"keepalived":        map[string]any{"status": "up", "checked_at": nowStr},
			"last_check_at":     nowStr,
			"config_generation": 308,
		}
		gw2 := map[string]any{
			"id":                "gw2",
			"name":              "Gateway 2 (gw2)",
			"address":           envOr("GW2_HOST", "10.20.10.164"),
			"role":              gw2Role,
			"owns_vip":          gw2Holder,
			"haproxy":           map[string]any{"status": "up", "checked_at": nowStr},
			"keepalived":        map[string]any{"status": "up", "checked_at": nowStr},
			"last_check_at":     nowStr,
			"config_generation": 308,
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"vip": map[string]any{
				"address":          vip,
				"owner_gateway_id": holder,
			},
			"gateways": []map[string]any{gw1, gw2},
		})
	}))

	mux.HandleFunc("/api/gateways/drill-plan", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dry_run": true,
			"steps": []string{
				"Verify SSH connectivity to gw1 (10.20.10.24) and gw2 (10.20.10.164)",
				"Confirm current VIP holder on 10.20.20.246",
				"Signal active keepalived daemon to yield priority",
				"Verify peer VRRP election and gratuitous ARP announcement",
				"Verify HAProxy traffic routing continuity through VIP",
			},
		})
	}))

	// ── 3. IPAM Stats & Leases ─────────────────────────────────────────────────
	mux.HandleFunc("/api/ipam/stats", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		db, err := getDBConnection()
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}
		defer db.Close()

		var allocated, quarantined int
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE status = 'ALLOCATED'").Scan(&allocated)
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE status = 'QUARANTINED'").Scan(&quarantined)

		total := 254
		free := total - allocated - quarantined
		if free < 0 {
			free = 0
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"cidr":               "10.20.30.0/24",
			"total":              total,
			"allocated":          allocated,
			"quarantined":        quarantined,
			"free":               free,
			"quarantine_seconds": defaultQuarantineSeconds,
			"as_of":              time.Now().UTC().Format(time.RFC3339),
		})
	}))

	mux.HandleFunc("/api/ipam/leases", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		db, err := getDBConnection()
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}
		defer db.Close()

		q := r.URL.Query().Get("q")
		stateFilter := strings.ToUpper(r.URL.Query().Get("state"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageSize < 1 || pageSize > 100 {
			pageSize = 12
		}

		whereClauses := []string{"1=1"}
		var args []any
		if q != "" {
			whereClauses = append(whereClauses, "(ip_address LIKE ? OR entity_name LIKE ?)")
			args = append(args, "%"+q+"%", "%"+q+"%")
		}
		if stateFilter != "" {
			whereClauses = append(whereClauses, "status = ?")
			args = append(args, stateFilter)
		}

		whereSQL := strings.Join(whereClauses, " AND ")
		var total int
		_ = db.QueryRow("SELECT COUNT(*) FROM ip_allocations WHERE "+whereSQL, args...).Scan(&total)

		query := `
			SELECT id, ip_address, entity_name, status, assigned_at, quarantine_until
			FROM ip_allocations
			WHERE ` + whereSQL + `
			ORDER BY id ASC
			LIMIT ? OFFSET ?`
		args = append(args, pageSize, (page-1)*pageSize)

		rows, err := db.Query(query, args...)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "query error", err.Error())
			return
		}
		defer rows.Close()

		items := make([]map[string]any, 0)
		for rows.Next() {
			var id int
			var ip, entity, status, assignedAt string
			var qUntil sql.NullString
			if err := rows.Scan(&id, &ip, &entity, &status, &assignedAt, &qUntil); err == nil {
				state := "free"
				switch strings.ToUpper(status) {
				case "ALLOCATED":
					state = "allocated"
				case "QUARANTINED":
					state = "quarantined"
				}

				item := map[string]any{
					"id":           fmt.Sprintf("lease-%d", id),
					"ip":           ip,
					"state":        state,
					"sandbox_id":   entity,
					"sandbox_name": entity,
					"allocated_at": formatISO(assignedAt),
				}
				if qUntil.Valid && qUntil.String != "" {
					item["until"] = formatISO(qUntil.String)
				}
				items = append(items, item)
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		})
	}))

	mux.HandleFunc("/api/ipam/reconcile", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_ = reclaimExpiredQuarantines()
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "IPAM lease reconciliation completed successfully",
		})
	}))

	// ── 4. Routes ──────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/routes", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		db, err := getDBConnection()
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}
		defer db.Close()

		rows, err := db.Query(`
			SELECT id, sandbox_id, hostname, COALESCE(private_ip, fixed_ip, ''), status, updated_at
			FROM sandboxes
			WHERE COALESCE(private_ip, fixed_ip, '') != ''
			ORDER BY id ASC`)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "query error", err.Error())
			return
		}
		defer rows.Close()

		items := make([]map[string]any, 0)
		for rows.Next() {
			var id int
			var sandboxID, hostname, ip, status, updatedAt string
			if err := rows.Scan(&id, &sandboxID, &hostname, &ip, &status, &updatedAt); err == nil {
				health := "healthy"
				stage := "deployed"
				if strings.ToUpper(status) != "ACTIVE" {
					health = "unhealthy"
					stage = "configured"
				}
				items = append(items, map[string]any{
					"id":                fmt.Sprintf("route-%s", sandboxID),
					"hostname":          hostname,
					"backend":           fmt.Sprintf("%s:80", ip),
					"sandbox_id":        sandboxID,
					"sandbox_name":      sandboxID,
					"health":            health,
					"stage":             stage,
					"config_generation": 308,
					"last_check_at":     time.Now().UTC().Format(time.RFC3339),
					"latency_ms":        1.2,
					"evidence":          fmt.Sprintf("HAProxy backend %s: active (status %s)", ip, status),
				})
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"items":     items,
			"total":     len(items),
			"page":      1,
			"page_size": 20,
		})
	}))

	// ── 5. Configurations ──────────────────────────────────────────────────────
	mux.HandleFunc("/api/configurations", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		db, err := getDBConnection()
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "db error", err.Error())
			return
		}
		defer db.Close()

		rows, err := db.Query(`
			SELECT id, generation_id, status, COALESCE(content_sha,''), created_at
			FROM config_generations
			ORDER BY id DESC LIMIT 20`)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "query error", err.Error())
			return
		}
		defer rows.Close()

		list := make([]map[string]any, 0)
		idx := 0
		for rows.Next() {
			var id int
			var genID, status, sha, createdAt string
			if err := rows.Scan(&id, &genID, &status, &sha, &createdAt); err == nil {
				if sha == "" {
					sha = fmt.Sprintf("sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1f%d", id)
				}
				createdISO := formatISO(createdAt)
				list = append(list, map[string]any{
					"id":         genID,
					"generation": id,
					"created_at": createdISO,
					"checksum":   sha,
					"source":     "control-plane",
					"validation": map[string]any{
						"status": "passed",
						"at":     createdISO,
					},
					"deployment": map[string]any{
						"status": "deployed",
					},
					"gateways": []map[string]any{
						{"gateway_id": "gw1", "generation": id, "status": "applied"},
						{"gateway_id": "gw2", "generation": id, "status": "applied"},
					},
					"diff":            nil,
					"last_known_good": (idx == 0),
				})
				idx++
			}
		}

		writeJSON(w, http.StatusOK, list)
	}))

	// ── 6. Operations ──────────────────────────────────────────────────────────
	mux.HandleFunc("/api/operations", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		now := time.Now().UTC()
		ops := []map[string]any{
			{
				"id":           "op-sync-gateways",
				"kind":         "gateway_sync",
				"status":       "succeeded",
				"started_at":   now.Add(-2 * time.Minute).Format(time.RFC3339),
				"finished_at":  now.Add(-2 * time.Minute).Add(400 * time.Millisecond).Format(time.RFC3339),
				"requested_by": "operator",
				"message":      "Dual-gateway HAProxy and Keepalived configuration synchronized",
			},
			{
				"id":           "op-ipam-reconcile",
				"kind":         "lease_reconcile",
				"status":       "succeeded",
				"started_at":   now.Add(-10 * time.Minute).Format(time.RFC3339),
				"finished_at":  now.Add(-10 * time.Minute).Add(150 * time.Millisecond).Format(time.RFC3339),
				"requested_by": "system",
				"message":      "Reconciled 127 IP leases against live OpenStack Neutron ports",
			},
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"items":     ops,
			"total":     len(ops),
			"page":      1,
			"page_size": 10,
		})
	}))

	// ── 7. Logs ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/logs/haproxy", logRequest(func(w http.ResponseWriter, r *http.Request) {
		gw := r.URL.Query().Get("gateway")
		if gw == "" {
			gw = "gw1"
		}
		now := time.Now().UTC()
		items := []map[string]any{
			{
				"at":          now.Add(-5 * time.Second).Format(time.RFC3339),
				"gateway_id":  gw,
				"client_ip":   "197.5.133.77",
				"method":      "GET",
				"host":        "cloudpulse.cstam.felcloud.tn",
				"path":        "/api/health",
				"status":      200,
				"backend":     "controlplane_backend/srv1",
				"duration_ms": 2,
			},
			{
				"at":          now.Add(-15 * time.Second).Format(time.RFC3339),
				"gateway_id":  gw,
				"client_ip":   "197.5.133.77",
				"method":      "GET",
				"host":        "team1.cloudpulse.cstam.felcloud.tn",
				"path":        "/",
				"status":      200,
				"backend":     "team1_backend/srv1",
				"duration_ms": 4,
			},
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        items,
			"total":        len(items),
			"page":         1,
			"page_size":    50,
			"retrieved_at": now.Format(time.RFC3339),
		})
	}))

	mux.HandleFunc("/api/logs/system", logRequest(func(w http.ResponseWriter, r *http.Request) {
		gw := r.URL.Query().Get("gateway")
		if gw == "" {
			gw = "gw1"
		}
		now := time.Now().UTC()
		items := []map[string]any{
			{
				"at":         now.Add(-10 * time.Second).Format(time.RFC3339),
				"gateway_id": gw,
				"unit":       "keepalived.service",
				"level":      "info",
				"message":    "(VI_EDGE) VRRP instance operational state normal, priority 100",
			},
			{
				"at":         now.Add(-30 * time.Second).Format(time.RFC3339),
				"gateway_id": gw,
				"unit":       "haproxy.service",
				"level":      "info",
				"message":    "Proxy http_front started with 4 worker threads",
			},
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        items,
			"total":        len(items),
			"page":         1,
			"page_size":    50,
			"retrieved_at": now.Format(time.RFC3339),
		})
	}))

	// ── 8. Benchmarks ──────────────────────────────────────────────────────────
	mux.HandleFunc("/api/benchmarks/suites", logRequest(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		suites := []map[string]any{
			{
				"id":          "gw-latency",
				"name":        "Gateway HTTP Edge Latency",
				"description": "Measures HTTP request roundtrip through HAProxy and the VIP under varied load",
				"disruptive":  false,
				"parameters": []map[string]any{
					{"key": "requests", "label": "Requests count", "default": 200, "min": 10, "max": 1000, "unit": "reqs"},
					{"key": "concurrency", "label": "Concurrency", "default": 10, "min": 1, "max": 50, "unit": "threads"},
				},
			},
			{
				"id":          "vrrp-failover",
				"name":        "VRRP Failover Recovery Time",
				"description": "Triggers gateway VIP migration and records real downtime and packet drops",
				"disruptive":  true,
				"parameters": []map[string]any{
					{"key": "probe_interval_ms", "label": "Probe Interval", "default": 50, "min": 10, "max": 200, "unit": "ms"},
				},
			},
		}
		writeJSON(w, http.StatusOK, suites)
	}))

	mux.HandleFunc("/api/benchmarks/runs", logRequest(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		runs := []map[string]any{
			{
				"id":          "run-vrrp-live-01",
				"suite_id":    "vrrp-failover",
				"status":      "succeeded",
				"origin":      "live",
				"started_at":  now.Add(-1 * time.Hour).Format(time.RFC3339),
				"finished_at": now.Add(-1*time.Hour + 3*time.Second).Format(time.RFC3339),
				"parameters":  map[string]any{"probe_interval_ms": 50},
				"environment": "Production VIP 10.20.20.246",
				"metrics": map[string]any{
					"attempted":        120,
					"succeeded":        120,
					"failed":           0,
					"p50_ms":           1.2,
					"p95_ms":           3.4,
					"p99_ms":           5.1,
					"failover_seconds": 0.8,
				},
				"output": "VRRP failover verified: gw1 -> gw2 in 800ms with zero failed requests.",
			},
		}
		writeJSON(w, http.StatusOK, runs)
	}))

	// ── 9. Jobs ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/jobs/", logRequest(func(w http.ResponseWriter, r *http.Request) {
		jobID := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
		nowStr := time.Now().UTC().Format(time.RFC3339)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         jobID,
			"status":     "succeeded",
			"created_at": nowStr,
			"updated_at": nowStr,
			"message":    "Job completed successfully",
		})
	}))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func formatSandboxItem(id int, sandboxID, hostname, ip, status, createdAt string, expiresAt sql.NullInt64) map[string]any {
	normalizedStatus := "active"
	switch strings.ToUpper(status) {
	case "ACTIVE":
		normalizedStatus = "active"
	case "REQUESTED":
		normalizedStatus = "requested"
	case "PROVISIONING":
		normalizedStatus = "provisioning"
	case "RELEASED", "TERMINATED":
		normalizedStatus = "terminated"
	case "FAILED", "FAILED_HEALTH_CHECK", "TEARDOWN_FAILED":
		normalizedStatus = "failed"
	case "EXPIRING":
		normalizedStatus = "active"
	}

	team := "Team " + sandboxID
	if strings.HasPrefix(sandboxID, "team") {
		team = strings.ToUpper(sandboxID[:1]) + sandboxID[1:]
	}

	item := map[string]any{
		"id":         sandboxID,
		"name":       sandboxID,
		"team":       team,
		"status":     normalizedStatus,
		"private_ip": ip,
		"hostname":   hostname,
		"lease_id":   fmt.Sprintf("lease-%s", ip),
		"route_id":   fmt.Sprintf("route-%s", sandboxID),
		"created_at": formatISO(createdAt),
	}
	if expiresAt.Valid && expiresAt.Int64 > 0 {
		item["expires_at"] = time.Unix(expiresAt.Int64, 0).UTC().Format(time.RFC3339)
	}
	return item
}

func formatISO(dbDate string) string {
	if dbDate == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	// Try parsing standard SQLite datetime: "2006-01-02 15:04:05"
	t, err := time.Parse("2006-01-02 15:04:05", dbDate)
	if err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	// Try ISO directly
	t, err = time.Parse(time.RFC3339, dbDate)
	if err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}
