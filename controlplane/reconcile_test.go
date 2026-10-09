package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconcileFindsFourDriftCategories(t *testing.T) {
	setupTestDB(t)
	mock := &MockProvisioner{Servers: map[string]bool{}}
	provisioner = mock
	for _, fixture := range []struct{ name, ip string }{
		{"missing-port", "10.20.30.60"},
		{"missing-server", "10.20.30.61"},
	} {
		if _, err := allocateLeaseTTL(fixture.name, fixture.name+".cstam.felcloud.tn", "sandbox", fixture.ip, 3600); err != nil {
			t.Fatal(err)
		}
	}
	db, _ := getDBConnection()
	if _, err := db.Exec(`UPDATE sandboxes SET server_id='gone-server',port_id='gone-port' WHERE sandbox_id='missing-server'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	mock.Ports = []SandboxPort{
		{ID: "team1-port", Name: "team1-sandbox-port", FixedIPs: []string{"10.20.30.37"}},
		{ID: "team2-port", Name: "team2-sandbox-port", FixedIPs: []string{"10.20.30.15"}},
		{ID: "port-orphan", Name: "orphan-sandbox-port", FixedIPs: []string{"10.20.30.44"}},
	}
	db, _ = getDBConnection()
	defer db.Close()
	report, err := reconcileNeutron(context.Background(), db, mock)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.MissingNeutronPorts) != 2 || len(report.OrphanNeutronPorts) != 1 || len(report.IPMismatches) != 2 || len(report.MissingServers) != 1 {
		t.Fatalf("unexpected reconciliation report: %+v", report)
	}
	team1, ok := findReconcileItem(report.IPMismatches, "team1")
	if !ok || team1.IP != "10.20.30.70" || team1.NeutronIP != "10.20.30.37" {
		t.Fatalf("team1 IP mismatch details incorrect: %+v", team1)
	}
	team2, ok := findReconcileItem(report.IPMismatches, "team2")
	if !ok || team2.IP != "10.20.30.192" || team2.NeutronIP != "10.20.30.15" {
		t.Fatalf("team2 IP mismatch details incorrect: %+v", team2)
	}
	w := httptest.NewRecorder()
	handleReconcile(w, httptest.NewRequest(http.MethodGet, "/api/reconcile", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/reconcile status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReconcileApplyOnlyExplicitSandboxFixes(t *testing.T) {
	setupTestDB(t)
	mock := &MockProvisioner{Servers: map[string]bool{}}
	provisioner = mock
	if _, err := allocateLeaseTTL("missing-server", "missing-server.cstam.felcloud.tn", "sandbox", "10.20.30.61", 3600); err != nil {
		t.Fatal(err)
	}
	db, _ := getDBConnection()
	if _, err := db.Exec(`UPDATE sandboxes SET server_id='missing-nova' WHERE sandbox_id='missing-server'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id,hostname,subnet_name,private_ip,status) VALUES('team1','team1.cstam.felcloud.tn','sandbox','10.20.30.70','ACTIVE')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	mock.Ports = []SandboxPort{{ID: "team1-port", Name: "team1-sandbox-port", FixedIPs: []string{"10.20.30.37"}}}
	requestBody, _ := json.Marshal(reconcileRequest{Actions: []ReconcileAction{{Type: "update_ip", SandboxID: "team1"}, {Type: "mark_released", SandboxID: "missing-server"}}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/reconcile/apply", bytes.NewReader(requestBody))
	handleReconcileApply(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	db, _ = getDBConnection()
	defer db.Close()
	var fixedIP, oldLease, newLease, protectedStatus, sandboxStatus string
	_ = db.QueryRow(`SELECT fixed_ip FROM sandboxes WHERE sandbox_id='team1'`).Scan(&fixedIP)
	_ = db.QueryRow(`SELECT status FROM ip_allocations WHERE ip_address='10.20.30.70'`).Scan(&oldLease)
	_ = db.QueryRow(`SELECT status FROM ip_allocations WHERE ip_address='10.20.30.37' AND entity_name='team1'`).Scan(&newLease)
	_ = db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name='keepalived-vip'`).Scan(&protectedStatus)
	_ = db.QueryRow(`SELECT status FROM sandboxes WHERE sandbox_id='missing-server'`).Scan(&sandboxStatus)
	if fixedIP != "10.20.30.37" || oldLease != "QUARANTINED" || newLease != "ALLOCATED" || protectedStatus != "ALLOCATED" || sandboxStatus != "RELEASED" {
		t.Fatalf("apply state fixed=%s old=%s new=%s protected=%s server-sandbox=%s", fixedIP, oldLease, newLease, protectedStatus, sandboxStatus)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.Deleted) != 0 {
		t.Fatalf("reconciliation apply performed resource deletion: %v", mock.Deleted)
	}
}

func TestReconcileApplyCannotClaimProtectedIP(t *testing.T) {
	setupTestDB(t)
	mock := &MockProvisioner{}
	provisioner = mock
	db, _ := getDBConnection()
	if _, err := db.Exec(`INSERT INTO sandboxes(sandbox_id,hostname,subnet_name,private_ip,status) VALUES('team1','team1.cstam.felcloud.tn','sandbox','10.20.30.70','ACTIVE')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO ip_allocations(ip_address,subnet_name,entity_name,entity_type,status) VALUES('10.20.30.37','sandbox','vip','VIP','RESERVED')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	mock.Ports = []SandboxPort{{ID: "team1-port", Name: "team1-sandbox-port", FixedIPs: []string{"10.20.30.37"}}}
	db, _ = getDBConnection()
	if err := applyReconcileIPUpdate(db, ReconcileItem{SandboxID: "team1", IP: "10.20.30.70", NeutronIP: "10.20.30.37"}); err == nil {
		db.Close()
		t.Fatal("expected protected VIP IP to be rejected")
	}
	db.Close()
	db, _ = getDBConnection()
	defer db.Close()
	var status string
	if err := db.QueryRow(`SELECT status FROM ip_allocations WHERE entity_name='vip'`).Scan(&status); err != nil || status != "RESERVED" {
		t.Fatalf("protected lease status=%q err=%v", status, err)
	}
}

func TestReconcileApplyRejectsOwnershipAndResourceIdentityFields(t *testing.T) {
	setupTestDB(t)
	provisioner = &MockProvisioner{}
	if _, err := allocateLeaseTTL("immutable-row", "immutable-row.cstam.felcloud.tn", "sandbox", "10.20.30.62", 3600); err != nil {
		t.Fatal(err)
	}
	tests := []string{"managed_by", "server_id", "port_id"}
	for _, field := range tests {
		t.Run(field, func(t *testing.T) {
			body := `{"actions":[{"type":"mark_failed","sandbox_id":"immutable-row","` + field + `":"attacker-value"}]}`
			rec := httptest.NewRecorder()
			handleReconcileApply(rec, httptest.NewRequest(http.MethodPost, "/api/reconcile/apply", bytes.NewBufferString(body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var managedBy string
	var serverID, portID sql.NullString
	if err := db.QueryRow(`SELECT managed_by,server_id,port_id FROM sandboxes WHERE sandbox_id='immutable-row'`).Scan(&managedBy, &serverID, &portID); err != nil {
		t.Fatal(err)
	}
	if managedBy != "external" || serverID.Valid || portID.Valid {
		t.Fatalf("reconcile changed protected fields: managed_by=%s server_id=%v port_id=%v", managedBy, serverID, portID)
	}
}
