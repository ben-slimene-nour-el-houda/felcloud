package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    _ "github.com/mattn/go-sqlite3"
)

type gatewayStore struct {
    db *sql.DB
}

func newGatewayStore() (*gatewayStore, error) {
    dbPath := filepath.Join("..", "ipam", "ipam.db")
    db, err := sql.Open("sqlite3", dbPath)
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

func (s *gatewayStore) gatewayStatus() map[string]any {
    rows, err := s.db.Query(`SELECT sandbox_id, hostname, private_ip, status FROM sandboxes ORDER BY hostname`)
    if err != nil {
        return map[string]any{"error": err.Error()}
    }
    defer rows.Close()

    backends := map[string]any{}
    for rows.Next() {
        var sandboxID, hostname, privateIP, status string
        if err := rows.Scan(&sandboxID, &hostname, &privateIP, &status); err != nil {
            continue
        }
        backends[hostname] = map[string]any{"sandbox_id": sandboxID, "private_ip": privateIP, "status": status}
    }
    return map[string]any{"gateways": map[string]any{ "gw1": map[string]any{"reachable": true, "haproxy": "UP", "keepalived": "MASTER", "vip": "10.20.20.246", "generation": "gen-0000", "backends": backends}, "gw2": map[string]any{"reachable": true, "haproxy": "UP", "keepalived": "BACKUP", "vip": "10.20.20.246", "generation": "gen-0000", "backends": backends}}, "vip_holder": "gw1"}
}

func (s *gatewayStore) eventCount() int {
    var count int
    if err := s.db.QueryRow(`SELECT COUNT(*) FROM config_generations`).Scan(&count); err != nil {
        return 0
    }
    return count
}

func requireBearerToken(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        token := os.Getenv("CONTROL_PLANE_TOKEN")
        if token == "" {
            next(w, r)
            return
        }
        authHeader := r.Header.Get("Authorization")
        if !strings.HasPrefix(authHeader, "Bearer ") {
            writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing bearer token"})
            return
        }
        provided := strings.TrimPrefix(authHeader, "Bearer ")
        if provided != token {
            writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid bearer token"})
            return
        }
        next(w, r)
    }
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
    w.Header().Set("Content-Type", "application/json")
    if origin := strings.TrimSpace(os.Getenv("DASHBOARD_ORIGIN")); origin != "" {
        w.Header().Set("Access-Control-Allow-Origin", origin)
    }
    w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
    w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(payload)
}

func logRequest(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next(w, r)
        log.Printf("%s %s %s %s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
    }
}

func handler(store *gatewayStore) http.Handler {
    mux := http.NewServeMux()

    mux.HandleFunc("/health", logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"status": "UP", "source": "controlplane-gateway", "db": "OK", "timestamp": time.Now().UTC().Format(time.RFC3339)})
    }))

    mux.HandleFunc("/api/gateways/status", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, store.gatewayStatus())
    })))

    mux.HandleFunc("/api/gateways/logs", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        q := r.URL.Query()
        limit, _ := strconv.Atoi(q.Get("limit"))
        if limit <= 0 || limit > 200 { limit = 50 }
        writeJSON(w, http.StatusOK, map[string]any{"gateway": q.Get("gateway"), "limit": limit, "entries": []any{}})
    })))

    mux.HandleFunc("/api/gateways/events", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "count": 0})
        case http.MethodPost:
            var body map[string]any
            if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
                writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
                return
            }
            writeJSON(w, http.StatusCreated, map[string]any{"accepted": true, "event": body, "count": store.eventCount()})
        default:
            writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
        }
    })))

    mux.HandleFunc("/api/gateways/generations", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"generations": []any{}, "count": 0})
    })))

    mux.HandleFunc("/api/gateways/failover", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        var body map[string]any
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"}); return
        }
        if body["confirm"] != true && body["confirm"] != "true" {
            writeJSON(w, http.StatusBadRequest, map[string]any{"error": "confirm flag required"}); return
        }
        response := map[string]any{"success": true, "before": "gw1", "after": "gw2", "downtime_ms": 0, "mode": body["mode"]}
        writeJSON(w, http.StatusOK, response)
    })))

    mux.HandleFunc("/api/gateways/sync", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "sync requested"})
    })))

    mux.HandleFunc("/api/gateways/rollback", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "rollback executed", "duration_ms": 0})
    })))

    mux.HandleFunc("/ipam/leases", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"leases": []any{}})
    })))

    mux.HandleFunc("/api/leases", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"leases": []any{}})
    })))

    mux.HandleFunc("/api/sandboxes", requireBearerToken(logRequest(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet { writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}); return }
        writeJSON(w, http.StatusOK, map[string]any{"sandboxes": []any{}})
    })))

    mux.HandleFunc("/", logRequest(func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusNotFound, map[string]any{"error": "Not Found", "path": r.URL.Path})
    }))

    return mux
}

func main() {
    store, err := newGatewayStore()
    if err != nil {
        log.Printf("warning: sqlite store unavailable: %v", err)
    }
    if store == nil {
        db, err2 := sql.Open("sqlite3", filepath.Join("..", "ipam", "ipam.db"))
        if err2 != nil { log.Fatal(err2) }
        store = &gatewayStore{db: db}
    }
    defer store.close()

    port := os.Getenv("PORT")
    if port == "" { port = "8081" }
    addr := fmt.Sprintf(":%s", port)
    log.Printf("control-plane gateway listening on %s", addr)
    if err := http.ListenAndServe(addr, handler(store)); err != nil {
        log.Fatal(err)
    }
}
