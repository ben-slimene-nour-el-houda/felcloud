package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var dnsLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var provisionNameLocks sync.Map
var gatewaySyncFunc = func(dryRun bool) (bool, string) { return syncHAProxyDualGateways(dryRun) }
var healthProbeTimeout = 90 * time.Second
var healthProbeRetryInterval = 500 * time.Millisecond
var provisionHealthProbe = runHealthProbe

func runHealthProbe(ctx context.Context, ip string) (bool, string) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil || parsedIP.To4() == nil || parsedIP.String() != ip {
		return false, "probe target must be a literal IPv4 address"
	}
	probeCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	lastResult := "probe did not run"
	for {
		result, err := healthProbeRunner(probeCtx, healthProbeMode, ip)
		if err != nil {
			lastResult = err.Error()
		} else {
			lastResult = "HTTP " + result
			if probeHTTPStatusOK(result) {
				return true, lastResult
			}
		}
		select {
		case <-probeCtx.Done():
			return false, lastResult
		case <-time.After(healthProbeRetryInterval):
		}
	}
}

type provisionSyncState struct {
	mu      sync.Mutex
	running bool
	pending bool
	waiters int
	done    chan struct{}
	ok      bool
	message string
}

var provisionSync provisionSyncState

var errProvisionClaimed = errors.New("sandbox provisioning was claimed by another request")

func ensureProvisionColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sandboxes)`)
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for name, decl := range map[string]string{"server_id": "TEXT", "port_id": "TEXT", "fixed_ip": "TEXT", "provisioned_at": "INTEGER"} {
		if !cols[name] {
			if _, err := db.Exec(`ALTER TABLE sandboxes ADD COLUMN ` + name + ` ` + decl); err != nil {
				return err
			}
		}
	}
	return nil
}

type provisionResponse struct {
	SandboxID string           `json:"sandbox_id"`
	Team      string           `json:"team"`
	FixedIP   string           `json:"fixed_ip"`
	ServerID  string           `json:"server_id"`
	PortID    string           `json:"port_id"`
	Hostname  string           `json:"hostname"`
	State     string           `json:"state"`
	Timings   map[string]int64 `json:"timings_ms"`
}

func provisionError(w http.ResponseWriter, status int, id, step string, err error) {
	writeProvisionError(w, status, id, step, err, "", "")
}

func provisionErrorWithProbe(w http.ResponseWriter, status int, id, step string, err error, mode, result string) {
	writeProvisionError(w, status, id, step, err, mode, result)
}

func writeProvisionError(w http.ResponseWriter, status int, id, step string, err error, mode, result string) {
	msg := "provisioning failed"
	if err != nil {
		msg = err.Error()
	}
	body := map[string]interface{}{"error": msg, "step": step, "sandbox_id": id, "timings_ms": zeroTimings()}
	if mode != "" {
		body["health_probe_mode"] = mode
		body["last_probe_result"] = result
	}
	sendJSON(w, status, body)
}

func existingProvision(team string) (status string, response provisionResponse, err error) {
	db, err := getDBConnection()
	if err != nil {
		return "", response, err
	}
	defer db.Close()
	var host, ip, serverID, portID, state string
	err = db.QueryRow(`SELECT hostname,COALESCE(fixed_ip,private_ip,''),COALESCE(server_id,''),COALESCE(port_id,''),status FROM sandboxes WHERE sandbox_id=?`, team).Scan(&host, &ip, &serverID, &portID, &state)
	if err == sql.ErrNoRows {
		return "", response, nil
	}
	if err != nil {
		return "", response, err
	}
	response = provisionResponse{SandboxID: team, Team: team, FixedIP: ip, ServerID: serverID, PortID: portID, Hostname: host, State: state, Timings: zeroTimings()}
	return state, response, nil
}

func zeroTimings() map[string]int64 {
	return map[string]int64{"allocate": 0, "port": 0, "server_active": 0, "health": 0, "sync": 0, "total": 0}
}

func persistProvisioning(team string, lease *LeaseResult) error {
	db, err := getDBConnection()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE sandboxes SET private_ip=?,fixed_ip=?,server_id=NULL,port_id=NULL,provisioned_at=NULL,updated_at=CURRENT_TIMESTAMP WHERE sandbox_id=? AND status='PROVISIONING'`, lease.IPAddress, lease.IPAddress, team)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errProvisionClaimed
	}
	recordEvent(tx, team, "PROVISION_STARTED", fmt.Sprintf("ip=%s", lease.IPAddress), 0)
	return tx.Commit()
}

func persistResources(team string, r *SandboxResources) error {
	db, err := getDBConnection()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE sandboxes SET server_id=?,port_id=?,fixed_ip=?,private_ip=?,
		managed_by=CASE WHEN ?<>'' THEN 'control-plane' ELSE managed_by END,
		updated_at=CURRENT_TIMESTAMP WHERE sandbox_id=? AND status='PROVISIONING'`, r.ServerID, r.PortID, r.FixedIP, r.FixedIP, r.ServerID, team)
	if err == nil {
		recordEvent(db, team, "PORT_CREATED", r.PortID, r.PortDurationMS)
		if r.ServerID != "" {
			recordEvent(db, team, "SERVER_CREATED", r.ServerID, r.ServerCreateDurationMS)
		}
	}
	return err
}

func markProvisionFailed(team, ip, step string, cause error) error {
	db, err := getDBConnection()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	until := time.Now().UTC().Add(time.Duration(defaultQuarantineSeconds) * time.Second).Format("2006-01-02 15:04:05")
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err = tx.Exec(`UPDATE sandboxes SET status='FAILED',updated_at=CURRENT_TIMESTAMP WHERE sandbox_id=? AND status IN ('PROVISIONING','ACTIVE')`, team); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE ip_allocations SET status='QUARANTINED',quarantined_at=?,quarantine_until=? WHERE entity_name=? AND ip_address=?`, now, until, team, ip); err != nil {
		return err
	}
	recordEvent(tx, team, "released", fmt.Sprintf("ip=%s,cooldown=%d", ip, defaultQuarantineSeconds), 0)
	errorMessage := ""
	if cause != nil {
		errorMessage = cause.Error()
	}
	recordEvent(tx, team, "FAILED", fmt.Sprintf("step=%s error=%s", step, errorMessage), 0)
	return tx.Commit()
}

func persistActive(team string, r *SandboxResources, durationMs int64) error {
	db, err := getDBConnection()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE sandboxes SET status='ACTIVE',server_id=?,port_id=?,fixed_ip=?,private_ip=?,provisioned_at=?,updated_at=CURRENT_TIMESTAMP WHERE sandbox_id=? AND status='PROVISIONING'`, r.ServerID, r.PortID, r.FixedIP, r.FixedIP, time.Now().UTC().Unix(), team)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("sandbox left PROVISIONING state")
	}
	recordEvent(tx, team, "ACTIVE", "server and health check ready", durationMs)
	return tx.Commit()
}

func runCoalescedProvisionSync(ctx context.Context) (bool, string) {
	provisionSync.mu.Lock()
	if provisionSync.running {
		provisionSync.pending = true
		provisionSync.waiters++
		done := provisionSync.done
		provisionSync.mu.Unlock()
		ok, message := waitProvisionSync(ctx, done)
		provisionSync.mu.Lock()
		provisionSync.waiters--
		provisionSync.mu.Unlock()
		return ok, message
	}
	provisionSync.running = true
	provisionSync.pending = false
	provisionSync.done = make(chan struct{})
	done := provisionSync.done
	provisionSync.mu.Unlock()
	go runProvisionSyncLoop(done)
	return waitProvisionSync(ctx, done)
}

func waitProvisionSync(ctx context.Context, done <-chan struct{}) (bool, string) {
	select {
	case <-done:
		provisionSync.mu.Lock()
		ok, msg := provisionSync.ok, provisionSync.message
		provisionSync.mu.Unlock()
		return ok, msg
	case <-ctx.Done():
		return false, ctx.Err().Error()
	}
}

func runProvisionSyncLoop(done chan struct{}) {
	var ok bool
	var msg string
	const maxExtraRuns = 2
	extraRuns := 0
	for {
		ok, msg = gatewaySyncFunc(false)
		provisionSync.mu.Lock()
		if provisionSync.pending && extraRuns < maxExtraRuns {
			provisionSync.pending = false
			extraRuns++
			provisionSync.mu.Unlock()
			continue
		}
		provisionSync.pending = false
		provisionSync.ok, provisionSync.message = ok, msg
		provisionSync.running = false
		close(done)
		provisionSync.mu.Unlock()
		return
	}
}

func provisionSandbox(w http.ResponseWriter, r *http.Request, req CreateReq) {
	team := req.Team
	lockAny, _ := provisionNameLocks.LoadOrStore(team, &sync.Mutex{})
	teamLock := lockAny.(*sync.Mutex)
	teamLock.Lock()
	defer teamLock.Unlock()
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), provisionTimeout)
	defer cancel()
	lastProbeResult := ""
	if state, old, err := existingProvision(team); err != nil {
		provisionError(w, http.StatusBadGateway, team, "database", err)
		return
	} else if state == "ACTIVE" {
		sendJSON(w, 200, old)
		return
	} else if state != "" && state != "FAILED" && state != "RELEASED" && state != "FAILED_HEALTH_CHECK" {
		provisionError(w, 409, team, "state", fmt.Errorf("sandbox is already %s", state))
		return
	}
	host := team + "." + routingDomain
	allocStart := time.Now()
	lease, err := allocateProvisionLease(team, host)
	allocMs := time.Since(allocStart).Milliseconds()
	if err != nil {
		provisionError(w, 409, team, "allocate", err)
		return
	}
	resources := &SandboxResources{FixedIP: lease.IPAddress}
	fail := func(code int, step string, cause error) {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		cleanupErr := provisioner.DeleteSandbox(cleanupCtx, resources.ServerID, resources.PortID)
		cleanupCancel()
		stateErr := markProvisionFailed(team, lease.IPAddress, step, cause)
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
		syncOK, syncMessage := runCoalescedProvisionSync(syncCtx)
		syncCancel()
		if cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("resource cleanup: %w", cleanupErr))
		}
		if stateErr != nil {
			cause = errors.Join(cause, fmt.Errorf("lease quarantine/state update: %w", stateErr))
		}
		if !syncOK {
			cause = errors.Join(cause, fmt.Errorf("route cleanup sync: %s", syncMessage))
		}
		if step == "health" || step == "health_timeout" {
			provisionErrorWithProbe(w, code, team, step, cause, healthProbeMode, lastProbeResult)
			return
		}
		provisionError(w, code, team, step, cause)
	}
	if err = persistProvisioning(team, lease); err != nil {
		if errors.Is(err, errProvisionClaimed) {
			provisionError(w, http.StatusConflict, team, "state", err)
			return
		}
		fail(http.StatusBadGateway, "state", err)
		return
	}
	req.SandboxID = team
	req.FixedIP = lease.IPAddress
	created, err := provisioner.CreateSandbox(ctx, req)
	portMs := int64(0)
	if created != nil {
		resources = created
		portMs = created.PortDurationMS
		if persistErr := persistResources(team, resources); persistErr != nil {
			fail(http.StatusBadGateway, "database", persistErr)
			return
		}
	}
	if err != nil {
		fail(http.StatusBadGateway, "openstack_create", err)
		return
	}
	if resources.ServerID == "" || resources.PortID == "" || resources.FixedIP != lease.IPAddress {
		fail(http.StatusBadGateway, "openstack_create", fmt.Errorf("provisioner returned incomplete or mismatched resources"))
		return
	}
	activeStart := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, getErr := provisioner.GetServer(ctx, resources.ServerID)
		if getErr != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fail(http.StatusGatewayTimeout, "server_timeout", ctx.Err())
				return
			}
			fail(http.StatusBadGateway, "server_status", getErr)
			return
		}
		switch strings.ToUpper(status) {
		case "ACTIVE":
			goto serverActive
		case "ERROR":
			fail(http.StatusBadGateway, "server_error", fmt.Errorf("Nova server entered ERROR"))
			return
		}
		select {
		case <-ctx.Done():
			code := http.StatusGatewayTimeout
			fail(code, "server_timeout", ctx.Err())
			return
		case <-ticker.C:
		}
	}
serverActive:
	activeMs := time.Since(activeStart).Milliseconds() + resources.ServerCreateDurationMS
	healthStart := time.Now()
	healthy, msg := provisionHealthProbe(ctx, resources.FixedIP)
	lastProbeResult = msg
	healthMs := time.Since(healthStart).Milliseconds()
	if !healthy {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fail(http.StatusGatewayTimeout, "health_timeout", ctx.Err())
			return
		}
		fail(http.StatusBadGateway, "health", fmt.Errorf("backend health check failed: %s", msg))
		return
	}
	recordEventForSandbox(team, "HEALTH_OK", msg, healthMs)
	if err := persistActive(team, resources, activeMs); err != nil {
		fail(http.StatusBadGateway, "database", err)
		return
	}
	syncStart := time.Now()
	synced, syncMsg := runCoalescedProvisionSync(ctx)
	syncMs := time.Since(syncStart).Milliseconds()
	syncDetail := "success"
	if !synced {
		syncDetail = syncMsg
	}
	recordEventForSandbox(team, "SYNC", syncDetail, syncMs)
	if !synced {
		code := http.StatusBadGateway
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = http.StatusGatewayTimeout
		}
		fail(code, "gateway_sync", errors.New(syncMsg))
		return
	}
	response := provisionResponse{SandboxID: team, Team: team, FixedIP: resources.FixedIP, ServerID: resources.ServerID, PortID: resources.PortID, Hostname: host, State: "ACTIVE", Timings: map[string]int64{"allocate": allocMs, "port": portMs, "server_active": activeMs, "health": healthMs, "sync": syncMs, "total": time.Since(start).Milliseconds()}}
	sendJSON(w, http.StatusCreated, response)
}
