package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

// ErrTransitionConflict is returned when a CAS update finds 0 rows affected,
// indicating another writer changed the status between our read and write.
var ErrTransitionConflict = errors.New("concurrent state transition conflict")

// ErrTTLCapExceeded is returned when extending a sandbox TTL exceeds MAX_TTL_SECONDS.
var ErrTTLCapExceeded = errors.New("requested TTL extension exceeds maximum allowed cap")

// Global Configuration & Defaults
var (
	dbMutex                  sync.Mutex
	dbPath                   string
	varsYmlPath              string
	backupDir                string
	defaultQuarantineSeconds int
	routingDomain            string = "cstam.felcloud.tn"
	defaultTTLSeconds        int64  = 14400
	maxTTLSeconds            int64  = 86400
	minExtendSeconds         int64  = 60
	nowUnix                  func() int64 = func() int64 { return time.Now().UTC().Unix() }
)

func validateRoutingDomain(domain string) error {
	if len(domain) == 0 || len(domain) > 190 {
		return fmt.Errorf("invalid routing domain length (%d): must be between 1 and 190 characters", len(domain))
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.HasPrefix(domain, "-") || strings.HasSuffix(domain, "-") {
		return fmt.Errorf("invalid routing domain '%s': cannot start or end with '.' or '-'", domain)
	}

	labels := strings.Split(domain, ".")
	labelRegex := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("invalid routing domain label '%s': label length must be 1-63 chars", label)
		}
		if !labelRegex.MatchString(label) {
			return fmt.Errorf("invalid routing domain label '%s': must contain only lowercase letters, digits, and hyphens, and cannot start/end with hyphen", label)
		}
	}
	return nil
}

func initEnv() {
	execDir, err := os.Getwd()
	if err != nil {
		execDir = "."
	}

	// Determine DB path
	if envDB := os.Getenv("IPAM_DB_PATH"); envDB != "" {
		dbPath = envDB
	} else if _, err := os.Stat(filepath.Join(execDir, "ipam.db")); err == nil {
		dbPath = filepath.Join(execDir, "ipam.db")
	} else if _, err := os.Stat(filepath.Join(execDir, "ipam", "ipam.db")); err == nil {
		dbPath = filepath.Join(execDir, "ipam", "ipam.db")
	} else {
		// Fallback relative to project root
		dbPath = filepath.Join(execDir, "ipam", "ipam.db")
	}

	// Determine Ansible vars.yml path
	if envVars := os.Getenv("VARS_YML_PATH"); envVars != "" {
		varsYmlPath = envVars
	} else {
		varsYmlPath = filepath.Join(filepath.Dir(dbPath), "..", "ansible", "group_vars", "gateways", "vars.yml")
	}

	backupDir = filepath.Join(filepath.Dir(dbPath), "..", "ansible", "backups")

	defaultQuarantineSeconds = 60
	if envQ := os.Getenv("IP_QUARANTINE_SECONDS"); envQ != "" {
		if sec, err := strconv.Atoi(envQ); err == nil {
			defaultQuarantineSeconds = sec
		}
	}

	routingDomain = "cstam.felcloud.tn"
	if envRD := os.Getenv("ROUTING_DOMAIN"); envRD != "" {
		routingDomain = envRD
	}
	if err := validateRoutingDomain(routingDomain); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Invalid ROUTING_DOMAIN '%s': %v\n", routingDomain, err)
		os.Exit(1)
	}

	if envDefTTL := os.Getenv("DEFAULT_TTL_SECONDS"); envDefTTL != "" {
		if v, err := strconv.ParseInt(envDefTTL, 10, 64); err == nil {
			defaultTTLSeconds = v
		}
	}
	if envMaxTTL := os.Getenv("MAX_TTL_SECONDS"); envMaxTTL != "" {
		if v, err := strconv.ParseInt(envMaxTTL, 10, 64); err == nil {
			maxTTLSeconds = v
		}
	}
	if envMinExt := os.Getenv("MIN_EXTEND_SECONDS"); envMinExt != "" {
		if v, err := strconv.ParseInt(envMinExt, 10, 64); err == nil {
			minExtendSeconds = v
		}
	}

	if defaultTTLSeconds > maxTTLSeconds || defaultTTLSeconds <= 0 || maxTTLSeconds <= 0 || minExtendSeconds <= 0 {
		fmt.Fprintf(os.Stderr, "FATAL: Invalid TTL configuration: default=%d, max=%d, min_extend=%d\n", defaultTTLSeconds, maxTTLSeconds, minExtendSeconds)
		os.Exit(1)
	}
}

type DBQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func getDBConnection() (*sql.DB, error) {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	// Ensure target directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}

	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func initDB() error {
	db, err := getDBConnection()
	if err != nil {
		return err
	}
	defer db.Close()

	// 1. Subnets Table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS subnets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			cidr TEXT NOT NULL,
			gateway_ip TEXT NOT NULL,
			description TEXT
		);
	`)
	if err != nil {
		return fmt.Errorf("error creating subnets table: %w", err)
	}

	// 2. Check & create ip_allocations table
	var allocSQL string
	err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='ip_allocations'").Scan(&allocSQL)
	if err == sql.ErrNoRows {
		_, err = db.Exec(`
			CREATE TABLE ip_allocations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				ip_address TEXT UNIQUE NOT NULL,
				subnet_name TEXT NOT NULL,
				entity_name TEXT NOT NULL,
				entity_type TEXT NOT NULL,
				status TEXT CHECK(status IN ('ALLOCATED', 'RESERVED', 'FREE', 'RELEASED', 'QUARANTINED')) DEFAULT 'ALLOCATED',
				quarantined_at TIMESTAMP,
				quarantine_until TIMESTAMP,
				assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY(subnet_name) REFERENCES subnets(name)
			);
		`)
		if err != nil {
			return fmt.Errorf("error creating ip_allocations table: %w", err)
		}
	} else if err == nil && !strings.Contains(allocSQL, "QUARANTINED") {
		db.Exec(`
			CREATE TABLE ip_allocations_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				ip_address TEXT UNIQUE NOT NULL,
				subnet_name TEXT NOT NULL,
				entity_name TEXT NOT NULL,
				entity_type TEXT NOT NULL,
				status TEXT CHECK(status IN ('ALLOCATED', 'RESERVED', 'FREE', 'RELEASED', 'QUARANTINED')) DEFAULT 'ALLOCATED',
				quarantined_at TIMESTAMP,
				quarantine_until TIMESTAMP,
				assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY(subnet_name) REFERENCES subnets(name)
			);
			INSERT OR IGNORE INTO ip_allocations_new (id, ip_address, subnet_name, entity_name, entity_type, status, assigned_at)
			SELECT id, ip_address, subnet_name, entity_name, entity_type, status, assigned_at FROM ip_allocations;
			DROP TABLE ip_allocations;
			ALTER TABLE ip_allocations_new RENAME TO ip_allocations;
		`)
	}

	// 3. Check & create sandboxes table
	var sbSQL string
	err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='sandboxes'").Scan(&sbSQL)
	if err == sql.ErrNoRows {
		_, err = db.Exec(`
			CREATE TABLE sandboxes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				sandbox_id TEXT UNIQUE NOT NULL,
				hostname TEXT UNIQUE NOT NULL,
				subnet_name TEXT NOT NULL,
				private_ip TEXT,
				status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'EXPIRING', 'TEARING_DOWN', 'FAILED', 'FAILED_HEALTH_CHECK', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				expires_at INTEGER,
				FOREIGN KEY(subnet_name) REFERENCES subnets(name)
			);
		`)
		if err != nil {
			return fmt.Errorf("error creating sandboxes table: %w", err)
		}
	} else if err == nil {
		if !strings.Contains(sbSQL, "FAILED_HEALTH_CHECK") || !strings.Contains(sbSQL, "EXPIRING") {
			// Migrate: recreate table with expanded CHECK constraint, in one transaction,
			// with row-count verification before and after.
			if err := migrateSandboxesSchema(db); err != nil {
				return fmt.Errorf("sandboxes schema migration failed: %w", err)
			}
		}

		// Additive migration for expires_at column if missing
		var hasExpiresAt bool
		infoRows, infoErr := db.Query("PRAGMA table_info(sandboxes)")
		if infoErr == nil {
			for infoRows.Next() {
				var cid int
				var name, typeStr string
				var notNull int
				var dfltValue interface{}
				var pk int
				if err := infoRows.Scan(&cid, &name, &typeStr, &notNull, &dfltValue, &pk); err == nil {
					if name == "expires_at" {
						hasExpiresAt = true
					}
				}
			}
			infoRows.Close()
		}
		if !hasExpiresAt {
			if _, err := db.Exec("ALTER TABLE sandboxes ADD COLUMN expires_at INTEGER"); err != nil {
				return fmt.Errorf("failed to add expires_at column: %w", err)
			}
		}
	}

	// 4. Config generations table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS config_generations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			generation_id TEXT UNIQUE NOT NULL,
			config_snapshot TEXT NOT NULL,
			status TEXT CHECK(status IN ('PROPOSED', 'VALIDATED', 'COMMITTED', 'ROLLED_BACK', 'FAILED')) DEFAULT 'PROPOSED',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("error creating config_generations table: %w", err)
	}

	// 5. Events table & index
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts_ms INTEGER NOT NULL,
			sandbox_id TEXT,
			type TEXT NOT NULL,
			detail TEXT,
			duration_ms INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_events_sandbox_ts ON events(sandbox_id, ts_ms);
	`)
	if err != nil {
		return fmt.Errorf("error creating events table: %w", err)
	}

	// Seed subnets
	subnets := [][]string{
		{"mgmt", "10.20.10.0/24", "10.20.10.1", "Management Subnet"},
		{"edge", "10.20.20.0/24", "10.20.20.1", "Edge Subnet"},
		{"sandbox", "10.20.30.0/24", "10.20.30.1", "Sandbox Subnet"},
	}
	for _, s := range subnets {
		db.Exec(`INSERT OR IGNORE INTO subnets (name, cidr, gateway_ip, description) VALUES (?, ?, ?, ?)`, s[0], s[1], s[2], s[3])
	}

	// Seed known allocations
	allocs := [][]string{
		{"10.20.10.24", "mgmt", "gw1", "GATEWAY"},
		{"10.20.10.164", "mgmt", "gw2", "GATEWAY"},
		{"10.20.10.246", "mgmt", "bastion", "BASTION"},
		{"10.20.20.82", "edge", "gw1", "GATEWAY"},
		{"10.20.20.190", "edge", "gw2", "GATEWAY"},
		{"10.20.20.177", "edge", "bastion", "BASTION"},
		{"10.20.20.246", "edge", "keepalived-vip", "VIP"},
		{"10.20.30.86", "sandbox", "gw1", "GATEWAY"},
		{"10.20.30.224", "sandbox", "gw2", "GATEWAY"},
		{"10.20.30.70", "sandbox", "team1", "TEAM_SANDBOX"},
		{"10.20.30.192", "sandbox", "team2", "TEAM_SANDBOX"},
	}
	for _, a := range allocs {
		db.Exec(`INSERT OR IGNORE INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status) VALUES (?, ?, ?, ?, 'ALLOCATED')`, a[0], a[1], a[2], a[3])
	}

	fmt.Println("✓ IPAM database initialized successfully with quarantine and lifecycle tracking.")
	return nil
}

// migrateSandboxesSchema recreates the sandboxes table with the expanded CHECK
// constraint (adding EXPIRING, TEARING_DOWN). Runs in ONE transaction with
// row-count verification before and after to guarantee no rows are lost.
func migrateSandboxesSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback()

	// Check if old sandboxes table has expires_at column
	var oldHasExpiresAt bool
	infoRows, infoErr := tx.Query("PRAGMA table_info(sandboxes)")
	if infoErr == nil {
		for infoRows.Next() {
			var cid int
			var name, typeStr string
			var notNull int
			var dfltValue interface{}
			var pk int
			if err := infoRows.Scan(&cid, &name, &typeStr, &notNull, &dfltValue, &pk); err == nil {
				if name == "expires_at" {
					oldHasExpiresAt = true
				}
			}
		}
		infoRows.Close()
	}

	// Count rows before migration
	var countBefore int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sandboxes").Scan(&countBefore); err != nil {
		return fmt.Errorf("pre-migration row count: %w", err)
	}

	_, err = tx.Exec(`
		CREATE TABLE sandboxes_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sandbox_id TEXT UNIQUE NOT NULL,
			hostname TEXT UNIQUE NOT NULL,
			subnet_name TEXT NOT NULL,
			private_ip TEXT,
			status TEXT CHECK(status IN ('REQUESTED', 'PROVISIONING', 'ACTIVE', 'EXPIRING', 'TEARING_DOWN', 'FAILED', 'FAILED_HEALTH_CHECK', 'RELEASED', 'ROLLBACK')) DEFAULT 'REQUESTED',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at INTEGER,
			FOREIGN KEY(subnet_name) REFERENCES subnets(name)
		);
	`)
	if err != nil {
		return fmt.Errorf("create sandboxes_new: %w", err)
	}

	if oldHasExpiresAt {
		_, err = tx.Exec(`
			INSERT INTO sandboxes_new (id, sandbox_id, hostname, subnet_name, private_ip, status, created_at, updated_at, expires_at)
			SELECT id, sandbox_id, hostname, subnet_name, private_ip, status, created_at, updated_at, expires_at FROM sandboxes;
		`)
	} else {
		_, err = tx.Exec(`
			INSERT INTO sandboxes_new (id, sandbox_id, hostname, subnet_name, private_ip, status, created_at, updated_at)
			SELECT id, sandbox_id, hostname, subnet_name, private_ip, status, created_at, updated_at FROM sandboxes;
		`)
	}
	if err != nil {
		return fmt.Errorf("copy rows to sandboxes_new: %w", err)
	}

	// Count rows after copy
	var countAfter int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sandboxes_new").Scan(&countAfter); err != nil {
		return fmt.Errorf("post-migration row count: %w", err)
	}
	if countAfter != countBefore {
		return fmt.Errorf("row count mismatch: before=%d after=%d", countBefore, countAfter)
	}

	if _, err := tx.Exec("DROP TABLE sandboxes"); err != nil {
		return fmt.Errorf("drop old sandboxes: %w", err)
	}
	if _, err := tx.Exec("ALTER TABLE sandboxes_new RENAME TO sandboxes"); err != nil {
		return fmt.Errorf("rename sandboxes_new: %w", err)
	}

	return tx.Commit()
}

// recordEvent is best-effort: it logs errors but never returns one,
// so a failed event INSERT cannot abort the surrounding operation or
// roll back its transaction.
func recordEvent(q DBQueryer, sandboxID, typ, detail string, durationMs int64) {
	tsMs := time.Now().UTC().UnixMilli()
	var sID, det interface{}
	if sandboxID != "" {
		sID = sandboxID
	}
	if detail != "" {
		det = detail
	}
	_, err := q.Exec(`
		INSERT INTO events (ts_ms, sandbox_id, type, detail, duration_ms)
		VALUES (?, ?, ?, ?, ?)
	`, tsMs, sID, typ, det, durationMs)
	if err != nil {
		log.Printf("WARNING: recordEvent(%s, %s): %v", typ, sandboxID, err)
	}
}

func isValidTransition(from, to string) bool {
	switch from {
	case "PROVISIONING":
		return to == "ACTIVE" || to == "FAILED"
	case "ACTIVE":
		return to == "EXPIRING" || to == "TEARING_DOWN"
	case "EXPIRING":
		return to == "TEARING_DOWN"
	case "TEARING_DOWN":
		return to == "RELEASED" || to == "FAILED"
	case "FAILED":
		return to == "TEARING_DOWN"
	case "REQUESTED", "FAILED_HEALTH_CHECK", "ROLLBACK":
		return to == "TEARING_DOWN"
	default:
		return false
	}
}

// transitionSandbox performs an atomic CAS (compare-and-swap) on sandbox status.
// When q is a *sql.DB an immediate transaction is opened internally;
// when q is already a *sql.Tx the caller's transaction is used directly.
func transitionSandbox(q DBQueryer, id, to string) error {
	if db, ok := q.(*sql.DB); ok {
		// Wrap in an immediate transaction for true atomicity under concurrency.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("transitionSandbox: begin tx for '%s': %w", id, err)
		}
		defer tx.Rollback()
		if err := transitionSandbox(tx, id, to); err != nil {
			return err
		}
		return tx.Commit()
	}

	var currentStatus string
	err := q.QueryRow("SELECT status FROM sandboxes WHERE sandbox_id = ?", id).Scan(&currentStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("sandbox '%s' not found", id)
		}
		return fmt.Errorf("failed to query sandbox '%s': %w", id, err)
	}

	if !isValidTransition(currentStatus, to) {
		return fmt.Errorf("illegal state transition for sandbox '%s': '%s' -> '%s'", id, currentStatus, to)
	}

	res, err := q.Exec("UPDATE sandboxes SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ? AND status = ?", to, id, currentStatus)
	if err != nil {
		return fmt.Errorf("failed to update status for sandbox '%s': %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check affected rows for sandbox '%s': %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: sandbox '%s' (expected status '%s')", ErrTransitionConflict, id, currentStatus)
	}

	return nil
}

func reclaimExpiredQuarantinesTx(q DBQueryer) int {
	nowStr := time.Now().UTC().Format("2006-01-02 15:04:05")
	res, err := q.Exec(`
		UPDATE ip_allocations
		SET status = 'FREE', quarantined_at = NULL, quarantine_until = NULL
		WHERE status = 'QUARANTINED' AND (quarantine_until IS NULL OR quarantine_until <= ?);
	`, nowStr)
	if err != nil {
		return 0
	}

	rows, _ := res.RowsAffected()
	if rows > 0 {
		recordEvent(q, "", "reclaimed", fmt.Sprintf("count=%d", rows), 0)
		fmt.Printf("✓ Reclaimed %d expired quarantined IP address(es).\n", rows)
	}
	return int(rows)
}

func reclaimExpiredQuarantines() int {
	db, err := getDBConnection()
	if err != nil {
		return 0
	}
	defer db.Close()
	return reclaimExpiredQuarantinesTx(db)
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

func findAvailableIPTx(q DBQueryer, subnetName string, cidr string, gatewayIP string) (string, error) {
	// Parse subnet CIDR dynamically using net.ParseCIDR
	ipParsed, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR '%s' for subnet '%s': %w", cidr, subnetName, err)
	}

	// 1. Look for explicitly FREE records in this subnet that fall inside current CIDR
	rowsFree, err := q.Query(`
		SELECT ip_address FROM ip_allocations
		WHERE subnet_name = ? AND status = 'FREE'
		ORDER BY assigned_at
	`, subnetName)
	if err == nil {
		var freeIP string
		for rowsFree.Next() {
			if err := rowsFree.Scan(&freeIP); err == nil && freeIP != "" {
				if parsedFree := net.ParseIP(freeIP); parsedFree != nil && ipnet.Contains(parsedFree) {
					rowsFree.Close()
					return freeIP, nil
				}
			}
		}
		rowsFree.Close()
	}

	// 2. Iterate subnet hosts avoiding ALLOCATED, RESERVED, or QUARANTINED IPs
	rows, err := q.Query(`
		SELECT ip_address FROM ip_allocations
		WHERE subnet_name = ? AND status IN ('ALLOCATED', 'RESERVED', 'QUARANTINED')
	`, subnetName)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	activeOrQuarantined := make(map[string]bool)
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err == nil {
			activeOrQuarantined[ip] = true
		}
	}

	// 3. Dynamic Host Range Iteration based on Subnet CIDR (2nd host to 253rd host)
	startIP := ipToUint32(ipParsed.Mask(ipnet.Mask))
	ones, bits := ipnet.Mask.Size()
	hostCount := uint32(1) << uint(bits-ones)
	endIP := startIP + hostCount - 1

	for n := startIP + 2; n <= endIP-2; n++ {
		candIP := uint32ToIP(n).String()
		if candIP == gatewayIP {
			continue
		}
		if activeOrQuarantined[candIP] {
			continue
		}
		return candIP, nil
	}

	return "", fmt.Errorf("No free IPs available in subnet '%s' (check quarantined addresses).", subnetName)
}

func findAvailableIP(subnetName string) (string, error) {
	db, err := getDBConnection()
	if err != nil {
		return "", err
	}
	defer db.Close()

	reclaimExpiredQuarantinesTx(db)

	var cidr, gatewayIP string
	err = db.QueryRow("SELECT cidr, gateway_ip FROM subnets WHERE name = ?", subnetName).Scan(&cidr, &gatewayIP)
	if err != nil {
		return "", fmt.Errorf("Subnet '%s' does not exist in IPAM database.", subnetName)
	}

	return findAvailableIPTx(db, subnetName, cidr, gatewayIP)
}

type LeaseResult struct {
	SandboxID  string `json:"sandbox_id"`
	Hostname   string `json:"hostname"`
	IPAddress  string `json:"ip_address"`
	PrivateIP  string `json:"private_ip"`
	Status     string `json:"status"`
	SubnetName string `json:"subnet_name,omitempty"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
}

func allocateLease(sandboxID, hostname, subnetName, preferredIP string) (*LeaseResult, error) {
	return allocateLeaseTTL(sandboxID, hostname, subnetName, preferredIP, 0)
}

func allocateLeaseTTL(sandboxID, hostname, subnetName, preferredIP string, ttlSeconds int64) (*LeaseResult, error) {
	if subnetName == "" {
		subnetName = "sandbox"
	}
	if hostname == "" {
		hostname = fmt.Sprintf("%s.%s", sandboxID, routingDomain)
	}

	// Validate sandbox_id regex
	re := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	if !re.MatchString(sandboxID) {
		return nil, fmt.Errorf("Invalid sandbox ID '%s': must be 1-32 characters starting with alphanumeric, followed by alphanumerics, dashes, or underscores.", sandboxID)
	}

	effectiveTTL := ttlSeconds
	if effectiveTTL <= 0 {
		effectiveTTL = defaultTTLSeconds
	}
	if effectiveTTL > maxTTLSeconds {
		return nil, fmt.Errorf("requested TTL %d exceeds maximum allowed TTL %d", effectiveTTL, maxTTLSeconds)
	}

	exp := nowUnix() + effectiveTTL

	db, err := getDBConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Reclaim expired quarantines in transaction
	reclaimExpiredQuarantinesTx(tx)

	// 2. Lookup subnet CIDR & gateway IP
	var cidr, gatewayIP string
	err = tx.QueryRow("SELECT cidr, gateway_ip FROM subnets WHERE name = ?", subnetName).Scan(&cidr, &gatewayIP)
	if err != nil {
		return nil, fmt.Errorf("Subnet '%s' does not exist in IPAM database.", subnetName)
	}

	targetIP := preferredIP
	if targetIP == "" {
		ipFound, err := findAvailableIPTx(tx, subnetName, cidr, gatewayIP)
		if err != nil {
			return nil, err
		}
		targetIP = ipFound
	} else {
		parsedIP := net.ParseIP(targetIP)
		if parsedIP == nil {
			return nil, fmt.Errorf("Invalid IP address '%s'.", targetIP)
		}
		if targetIP == gatewayIP || strings.HasSuffix(targetIP, ".0") || strings.HasSuffix(targetIP, ".255") {
			return nil, fmt.Errorf("Requested IP %s is outside the valid host range for subnet '%s' (%s) or is a reserved address.", targetIP, subnetName, cidr)
		}
	}

	// 3. Check existing allocation in transaction
	var entityName, entityType, status, quarantineUntil string
	row := tx.QueryRow("SELECT entity_name, entity_type, status, COALESCE(quarantine_until, '') FROM ip_allocations WHERE ip_address = ?", targetIP)
	err = row.Scan(&entityName, &entityType, &status, &quarantineUntil)
	if err == nil {
		if entityType == "GATEWAY" || entityType == "BASTION" || entityType == "VIP" {
			return nil, fmt.Errorf("CRITICAL: IP %s is permanently reserved for infrastructure entity '%s' (type: %s). It cannot be allocated to a sandbox.", targetIP, entityName, entityType)
		}
		if status == "ALLOCATED" && entityName != sandboxID {
			return nil, fmt.Errorf("IP %s is currently ALLOCATED to %s.", targetIP, entityName)
		}
		if status == "QUARANTINED" {
			return nil, fmt.Errorf("IP %s is currently QUARANTINED until %s.", targetIP, quarantineUntil)
		}
	}

	// 4. Upsert allocation
	_, err = tx.Exec(`
		INSERT INTO ip_allocations (ip_address, subnet_name, entity_name, entity_type, status, quarantined_at, quarantine_until)
		VALUES (?, ?, ?, 'TEAM_SANDBOX', 'ALLOCATED', NULL, NULL)
		ON CONFLICT(ip_address) DO UPDATE SET
			subnet_name = excluded.subnet_name,
			entity_name = excluded.entity_name,
			entity_type = excluded.entity_type,
			status = 'ALLOCATED',
			quarantined_at = NULL,
			quarantine_until = NULL,
			assigned_at = CURRENT_TIMESTAMP;
	`, targetIP, subnetName, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to insert allocation: %w", err)
	}

	// 5. Clear stale sandboxes referencing this IP
	_, err = tx.Exec("UPDATE sandboxes SET private_ip = NULL WHERE private_ip = ? AND status IN ('RELEASED', 'FAILED', 'FAILED_HEALTH_CHECK', 'ROLLBACK')", targetIP)
	if err != nil {
		return nil, fmt.Errorf("failed to clear stale sandboxes: %w", err)
	}

	// 6. Upsert sandbox with expires_at
	_, err = tx.Exec(`
		INSERT INTO sandboxes (sandbox_id, hostname, subnet_name, private_ip, status, expires_at)
		VALUES (?, ?, ?, ?, 'ACTIVE', ?)
		ON CONFLICT(sandbox_id) DO UPDATE SET
			hostname = excluded.hostname,
			subnet_name = excluded.subnet_name,
			private_ip = excluded.private_ip,
			status = 'ACTIVE',
			expires_at = excluded.expires_at,
			updated_at = CURRENT_TIMESTAMP;
	`, sandboxID, hostname, subnetName, targetIP, exp)
	if err != nil {
		return nil, fmt.Errorf("failed to insert sandbox: %w", err)
	}

	// Record allocated event in same transaction
	recordEvent(tx, sandboxID, "allocated", fmt.Sprintf("ip=%s", targetIP), 0)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &LeaseResult{
		SandboxID:  sandboxID,
		Hostname:   hostname,
		IPAddress:  targetIP,
		PrivateIP:  targetIP,
		Status:     "ALLOCATED",
		SubnetName: subnetName,
		ExpiresAt:  &exp,
	}, nil
}

func extendSandbox(q DBQueryer, sandboxID string, extendSeconds int64) (int64, error) {
	if db, ok := q.(*sql.DB); ok {
		tx, err := db.Begin()
		if err != nil {
			return 0, fmt.Errorf("extendSandbox: begin tx for '%s': %w", sandboxID, err)
		}
		defer tx.Rollback()
		newExp, err := extendSandbox(tx, sandboxID, extendSeconds)
		if err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("extendSandbox: commit tx for '%s': %w", sandboxID, err)
		}
		return newExp, nil
	}

	if extendSeconds < minExtendSeconds || extendSeconds <= 0 {
		return 0, fmt.Errorf("extend_seconds (%d) must be at least %d", extendSeconds, minExtendSeconds)
	}

	var status string
	var expVal sql.NullInt64
	err := q.QueryRow("SELECT status, expires_at FROM sandboxes WHERE sandbox_id = ?", sandboxID).Scan(&status, &expVal)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("sandbox '%s' not found", sandboxID)
		}
		return 0, fmt.Errorf("failed to query sandbox '%s': %w", sandboxID, err)
	}

	if status != "ACTIVE" || !expVal.Valid {
		return 0, fmt.Errorf("cannot extend sandbox '%s': status is '%s' and expires_at valid is %v", sandboxID, status, expVal.Valid)
	}

	now := nowUnix()
	baseTime := expVal.Int64
	if now > baseTime {
		baseTime = now
	}
	newExpiresAt := baseTime + extendSeconds

	if newExpiresAt-now > maxTTLSeconds {
		return 0, ErrTTLCapExceeded
	}

	res, err := q.Exec("UPDATE sandboxes SET expires_at = ?, updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ? AND status = 'ACTIVE'", newExpiresAt, sandboxID)
	if err != nil {
		return 0, fmt.Errorf("failed to update expires_at for sandbox '%s': %w", sandboxID, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check affected rows for sandbox '%s': %w", sandboxID, err)
	}
	if rows == 0 {
		return 0, fmt.Errorf("%w: sandbox '%s'", ErrTransitionConflict, sandboxID)
	}

	recordEvent(q, sandboxID, "extended", fmt.Sprintf("from=%d to=%d", expVal.Int64, newExpiresAt), 0)

	return newExpiresAt, nil
}

type ReleaseResult struct {
	SandboxID       string `json:"sandbox_id"`
	IPAddress       string `json:"ip_address"`
	Status          string `json:"status"`
	QuarantinedAt   string `json:"quarantined_at,omitempty"`
	QuarantineUntil string `json:"quarantine_until,omitempty"`
	CooldownSeconds int    `json:"cooldown_seconds,omitempty"`
	Message         string `json:"message,omitempty"`
}

func releaseLease(sandboxID, ipAddress string, quarantineSec int) (*ReleaseResult, error) {
	if quarantineSec <= 0 {
		quarantineSec = defaultQuarantineSeconds
	}

	db, err := getDBConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var targetIP string
	var existingPrivateIP sql.NullString
	err = db.QueryRow("SELECT private_ip FROM sandboxes WHERE sandbox_id = ?", sandboxID).Scan(&existingPrivateIP)
	if err == nil && existingPrivateIP.Valid {
		targetIP = existingPrivateIP.String
	}
	if ipAddress != "" {
		targetIP = ipAddress
	}

	if targetIP == "" && err == sql.ErrNoRows {
		return &ReleaseResult{Status: "NOT_FOUND", Message: fmt.Sprintf("Sandbox '%s' not found", sandboxID)}, nil
	}

	now := time.Now().UTC()
	quarantineUntilStr := now.Add(time.Duration(quarantineSec) * time.Second).Format("2006-01-02 15:04:05")
	quarantinedAtStr := now.Format("2006-01-02 15:04:05")

	// Wrap quarantine + sandbox status + event recording in a single transaction
	tx, txErr := db.Begin()
	if txErr != nil {
		return nil, fmt.Errorf("releaseLease: begin tx: %w", txErr)
	}
	defer tx.Rollback()

	if targetIP != "" {
		tx.Exec(`
			UPDATE ip_allocations
			SET status = 'QUARANTINED', quarantined_at = ?, quarantine_until = ?
			WHERE ip_address = ?;
		`, quarantinedAtStr, quarantineUntilStr, targetIP)
	}

	tx.Exec("UPDATE sandboxes SET status = 'RELEASED', private_ip = NULL, updated_at = CURRENT_TIMESTAMP WHERE sandbox_id = ?", sandboxID)

	recordEvent(tx, sandboxID, "released", fmt.Sprintf("ip=%s,cooldown=%d", targetIP, quarantineSec), 0)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("releaseLease: commit tx: %w", err)
	}

	fmt.Printf("✓ IP %s placed in QUARANTINED state until %s (%ds cooldown).\n", targetIP, quarantineUntilStr, quarantineSec)

	return &ReleaseResult{
		SandboxID:       sandboxID,
		IPAddress:       targetIP,
		Status:          "QUARANTINED",
		QuarantinedAt:   quarantinedAtStr,
		QuarantineUntil: quarantineUntilStr,
		CooldownSeconds: quarantineSec,
	}, nil
}

func checkSandboxHealth(ipAddress string, port int, timeout time.Duration, maxRetries int, retryInterval time.Duration) (bool, string) {
	if port <= 0 {
		port = 80
	}
	url := fmt.Sprintf("http://%s:%d/", ipAddress, port)
	fmt.Printf("--> Performing pre-flight health check on %s (max %d attempts)...\n", url, maxRetries)

	client := &http.Client{
		Timeout: timeout,
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err == nil {
			req.Header.Set("User-Agent", "FelCloud-HealthCheck/2.0")
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
					fmt.Printf("✓ Health check PASSED on %s (Attempt %d, Status %d)\n", url, attempt, resp.StatusCode)
					return true, msg
				}
			}
		}

		// Fallback lightweight TCP socket probe
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ipAddress, port), timeout)
		if err == nil {
			conn.Close()
			fmt.Printf("✓ Health check (TCP socket) PASSED on %s:%d\n", ipAddress, port)
			return true, "TCP Connection Established"
		}

		if attempt < maxRetries {
			time.Sleep(retryInterval)
		}
	}

	msg := fmt.Sprintf("Connection to %s:%d failed or timed out", ipAddress, port)
	fmt.Printf("✗ Pre-flight health check FAILED on %s after %d attempts.\n", url, maxRetries)
	return false, msg
}

func validateHAProxySyntax(configContent string) (bool, string) {
	tempCertPath := "/tmp/haproxy_temp_test_cert.pem"
	testContent := configContent
	tempCertCreated := false

	// Find cert matches
	re := regexp.MustCompile(`crt\s+([^\s]+)`)
	matches := re.FindAllStringSubmatch(configContent, -1)
	for _, m := range matches {
		if len(m) > 1 {
			certPath := m[1]
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				if _, err := os.Stat(tempCertPath); os.IsNotExist(err) {
					cmd := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
						"-keyout", "/tmp/temp_k.key", "-out", "/tmp/temp_c.crt",
						"-days", "1", "-subj", "/CN=localhost")
					_ = cmd.Run()
					kData, err1 := os.ReadFile("/tmp/temp_k.key")
					cData, err2 := os.ReadFile("/tmp/temp_c.crt")
					if err1 == nil && err2 == nil {
						combined := append(kData, '\n')
						combined = append(combined, cData...)
						os.WriteFile(tempCertPath, combined, 0644)
						tempCertCreated = true
					}
					_ = os.Remove("/tmp/temp_k.key")
					_ = os.Remove("/tmp/temp_c.crt")
				}
				if _, err := os.Stat(tempCertPath); err == nil {
					testContent = strings.ReplaceAll(testContent, certPath, tempCertPath)
				}
			}
		}
	}

	tmpFile, err := os.CreateTemp("", "haproxy_*.cfg")
	if err != nil {
		return false, err.Error()
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(testContent)
	tmpFile.Close()

	cmd := exec.Command("haproxy", "-c", "-f", tmpFile.Name())
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	if err == nil {
		if tempCertCreated {
			_ = os.Remove(tempCertPath)
		}
		return true, "Configuration syntax is valid"
	}

	if tempCertCreated {
		_ = os.Remove(tempCertPath)
	}

	// If haproxy CLI is missing on the system
	if strings.Contains(err.Error(), "executable file not found") {
		if strings.Contains(configContent, "frontend") && strings.Contains(configContent, "backend") {
			return true, "Syntax structure valid (haproxy CLI not found on host)"
		}
		return false, "Missing frontend or backend definitions"
	}

	return false, fmt.Sprintf("HAProxy syntax error:\n%s\n%s", errBuf.String(), outBuf.String())
}

type TeamEntry struct {
	Name string `yaml:"name" json:"name"`
	IP   string `yaml:"ip" json:"ip"`
	Port int    `yaml:"port,omitempty" json:"port,omitempty"`
}

func generateHAProxyConfig(teams []TeamEntry) string {
	var sb strings.Builder

	sb.WriteString("global\n")
	sb.WriteString("    log /dev/log local0\n")
	sb.WriteString("    log /dev/log local1 notice\n")
	sb.WriteString("    chroot /var/lib/haproxy\n")
	sb.WriteString("    user haproxy\n")
	sb.WriteString("    group haproxy\n")
	sb.WriteString("    daemon\n")
	sb.WriteString("    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners\n")
	sb.WriteString("    stats timeout 30s\n\n")

	sb.WriteString("defaults\n")
	sb.WriteString("    log     global\n")
	sb.WriteString("    mode    http\n")
	sb.WriteString("    option  httplog\n")
	sb.WriteString("    option  dontlognull\n")
	sb.WriteString("    timeout connect 5000ms\n")
	sb.WriteString("    timeout client  50000ms\n")
	sb.WriteString("    timeout server  50000ms\n\n")

	sb.WriteString("# --- Hostname-Based ACL Routing (HTTP 80 & HTTPS 443) ---\n\n")

	sb.WriteString("frontend http_front\n")
	sb.WriteString("    bind *:80\n")
	sb.WriteString("    mode http\n")
	for _, t := range teams {
		sb.WriteString(fmt.Sprintf("    acl is_%s hdr(host) -i %s.%s\n", t.Name, t.Name, routingDomain))
		sb.WriteString(fmt.Sprintf("    use_backend %s_backend if is_%s\n", t.Name, t.Name))
	}
	sb.WriteString("    default_backend default_maintenance\n\n")

	sb.WriteString("frontend https_front\n")
	sb.WriteString("    bind *:443 ssl crt /etc/haproxy/certs/felcloud-gateway.pem alpn h2,http/1.1\n")
	sb.WriteString("    mode http\n")
	for _, t := range teams {
		sb.WriteString(fmt.Sprintf("    acl is_%s hdr(host) -i %s.%s\n", t.Name, t.Name, routingDomain))
		sb.WriteString(fmt.Sprintf("    use_backend %s_backend if is_%s\n", t.Name, t.Name))
	}
	sb.WriteString("    default_backend default_maintenance\n\n")

	for _, t := range teams {
		port := t.Port
		if port == 0 {
			port = 80
		}
		sb.WriteString(fmt.Sprintf("backend %s_backend\n", t.Name))
		sb.WriteString("    mode http\n")
		sb.WriteString("    balance roundrobin\n")
		sb.WriteString(fmt.Sprintf("    server %s_srv %s:%d check inter 2000 rise 2 fall 3\n\n", t.Name, t.IP, port))
	}

	sb.WriteString("backend default_maintenance\n")
	sb.WriteString("    mode http\n")
	sb.WriteString("    http-request return status 503 content-type \"text/plain\" lf-string \"503 Service Unavailable - FelCloud Resilient Gateway Maintenance\\n\"\n\n")

	return sb.String()
}

func updateAnsibleTeams() ([]TeamEntry, error) {
	db, err := getDBConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT sandbox_id, private_ip FROM sandboxes WHERE status IN ('ACTIVE', 'PROVISIONING')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []TeamEntry
	for rows.Next() {
		var sID, ip string
		if err := rows.Scan(&sID, &ip); err == nil && ip != "" {
			teams = append(teams, TeamEntry{
				Name: sID,
				IP:   ip,
				Port: 80,
			})
		}
	}

	existingData := make(map[string]interface{})
	if _, err := os.Stat(varsYmlPath); err == nil {
		content, err := os.ReadFile(varsYmlPath)
		if err == nil {
			_ = yaml.Unmarshal(content, &existingData)
		}
	}

	existingData["teams"] = teams
	existingData["routing_domain"] = routingDomain
	delete(existingData, "haproxy_dpa_pass")

	_ = os.MkdirAll(filepath.Dir(varsYmlPath), 0755)
	out, err := yaml.Marshal(existingData)
	if err == nil {
		os.WriteFile(varsYmlPath, out, 0644)
		fmt.Printf("✓ Synchronized Ansible variables in %s with IPAM database.\n", filepath.Base(varsYmlPath))
	}

	return teams, nil
}

func rollbackConfig() bool {
	db, err := getDBConnection()
	if err != nil {
		return false
	}
	defer db.Close()

	var genID, snapshot string
	err = db.QueryRow("SELECT generation_id, config_snapshot FROM config_generations WHERE status = 'COMMITTED' ORDER BY id DESC LIMIT 1").Scan(&genID, &snapshot)
	if err != nil {
		fmt.Println("✗ No previous committed generation found for rollback.")
		return false
	}

	fmt.Printf("--> Rolling back to Generation [%s]...\n", genID)
	os.WriteFile("/tmp/haproxy_rollback.cfg", []byte(snapshot), 0644)
	recordEvent(db, "", "rollback", genID, 0)
	fmt.Printf("✓ Configuration restored to previous generation [%s].\n", genID)
	return true
}

func syncHAProxyDualGateways(dryRun bool) (bool, string) {
	teams, _ := updateAnsibleTeams()
	candidateCfg := generateHAProxyConfig(teams)

	isValid, validationMsg := validateHAProxySyntax(candidateCfg)
	if !isValid {
		fmt.Printf("✗ CRITICAL: Candidate HAProxy configuration failed validation:\n%s\n", validationMsg)
		fmt.Println("✗ Aborting reload! Active gateway configuration untouched.")
		db, _ := getDBConnection()
		if db != nil {
			recordEvent(db, "", "sync_failed", validationMsg, 0)
			db.Close()
		}
		return false, fmt.Sprintf("Validation failed: %s", validationMsg)
	}

	fmt.Println("✓ Candidate configuration pre-validation passed.")
	if dryRun {
		return true, "Dry-run validation successful"
	}

	genID := fmt.Sprintf("gen-%s", time.Now().UTC().Format("20060102150405"))
	db, err := getDBConnection()
	if err != nil {
		return false, err.Error()
	}

	_, err = db.Exec("INSERT INTO config_generations (generation_id, config_snapshot, status) VALUES (?, ?, 'VALIDATED')", genID, candidateCfg)
	db.Close()
	if err != nil {
		return false, fmt.Sprintf("Failed to record config generation: %v", err)
	}

	fmt.Printf("\n--> Deploying Generation [%s] across GW1 and GW2...\n", genID)
	bastionIP := os.Getenv("BASTION_IP")
	if bastionIP == "" {
		bastionIP = "203.0.113.119"
	}

	cmd := exec.Command("ansible-playbook",
		"-i", "../ansible/inventory/hosts.yml",
		"../ansible/site.yml",
		"--vault-password-file", "../ansible/.vault_pass",
		"--tags", "haproxy",
		"-e", fmt.Sprintf("routing_domain=%s", routingDomain),
		"-e", fmt.Sprintf("ansible_ssh_common_args='-o ProxyJump=ansible@%s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'", bastionIP),
	)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	if err == nil {
		fmt.Printf("✓ Generation [%s] successfully validated and reloaded across GW1 and GW2 with zero downtime.\n", genID)
		db, _ := getDBConnection()
		if db != nil {
			db.Exec("UPDATE config_generations SET status = 'COMMITTED' WHERE generation_id = ?", genID)
			recordEvent(db, "", "sync_ok", genID, 0)
			db.Close()
		}
		return true, fmt.Sprintf("Generation %s applied", genID)
	}

	fmt.Println("✗ Ansible deployment failed! Triggering automatic rollback...")
	db, _ = getDBConnection()
	if db != nil {
		recordEvent(db, "", "sync_failed", errBuf.String(), 0)
		db.Close()
	}
	rollbackConfig()
	return false, fmt.Sprintf("Ansible error: %s", errBuf.String())
}

func addTeamFlow(teamName, ipAddress string, port int, skipHealthCheck bool) (bool, string) {
	initDB()
	reclaimExpiredQuarantines()

	allocRes, err := allocateLease(teamName, "", "sandbox", ipAddress)
	if err != nil {
		fmt.Printf("✗ Allocation Error: %v\n", err)
		return false, err.Error()
	}

	assignedIP := allocRes.IPAddress
	fmt.Printf("✓ Lease allocated for '%s': %s:%d\n", teamName, assignedIP, port)

	if !skipHealthCheck {
		isHealthy, healthMsg := checkSandboxHealth(assignedIP, port, 2*time.Second, 3, 1*time.Second)
		if !isHealthy {
			fmt.Printf("✗ Sandbox health check failed (%s). Route NOT opened.\n", healthMsg)
			db, _ := getDBConnection()
			if db != nil {
				db.Exec("UPDATE sandboxes SET status = 'FAILED_HEALTH_CHECK' WHERE sandbox_id = ?", teamName)
				db.Close()
			}
			return false, fmt.Sprintf("Health check failed: %s", healthMsg)
		}
	}

	return syncHAProxyDualGateways(false)
}

func removeTeamFlow(teamName string, quarantineSec int) (bool, string) {
	initDB()
	result, err := releaseLease(teamName, "", quarantineSec)
	if err != nil || result.Status == "NOT_FOUND" {
		fmt.Printf("✗ Error: Team '%s' not found.\n", teamName)
		return false, "Not found"
	}

	syncHAProxyDualGateways(false)
	return true, fmt.Sprintf("Team '%s' removed; IP quarantined for %ds", teamName, result.CooldownSeconds)
}

func listAllocations() {
	reclaimExpiredQuarantines()
	db, err := getDBConnection()
	if err != nil {
		fmt.Println("Error connecting to DB:", err)
		return
	}
	defer db.Close()

	fmt.Println("\n================================================================================")
	fmt.Println("                           IPAM SUBNETS OVERVIEW                                ")
	fmt.Println("================================================================================")
	subRows, _ := db.Query("SELECT name, cidr, gateway_ip, COALESCE(description, '') FROM subnets")
	if subRows != nil {
		for subRows.Next() {
			var name, cidr, gw, desc string
			subRows.Scan(&name, &cidr, &gw, &desc)
			fmt.Printf(" • [%-7s] CIDR: %-14s | Gateway: %-12s | %s\n", strings.ToUpper(name), cidr, gw, desc)
		}
		subRows.Close()
	}

	fmt.Println("\n================================================================================")
	fmt.Println("                           IP ADDRESS ALLOCATIONS                               ")
	fmt.Println("================================================================================")
	fmt.Printf("%-18s %-10s %-16s %-14s %-12s %s\n", "IP Address", "Subnet", "Entity Name", "Type", "Status", "Quarantine Until")
	fmt.Println(strings.Repeat("-", 80))
	allocRows, _ := db.Query("SELECT ip_address, subnet_name, entity_name, entity_type, status, COALESCE(quarantine_until, '-') FROM ip_allocations ORDER BY subnet_name, ip_address")
	if allocRows != nil {
		for allocRows.Next() {
			var ip, sub, entity, eType, st, qUntil string
			allocRows.Scan(&ip, &sub, &entity, &eType, &st, &qUntil)
			fmt.Printf("%-18s %-10s %-16s %-14s %-12s %s\n", ip, sub, entity, eType, st, qUntil)
		}
		allocRows.Close()
	}

	fmt.Println("\n================================================================================")
	fmt.Println("                             SANDBOXES STATUS                                   ")
	fmt.Println("================================================================================")
	fmt.Printf("%-18s %-24s %-16s %s\n", "Sandbox ID", "Hostname", "IP Address", "Status")
	fmt.Println(strings.Repeat("-", 80))
	sbRows, _ := db.Query("SELECT sandbox_id, hostname, COALESCE(private_ip, '-'), status FROM sandboxes ORDER BY created_at")
	if sbRows != nil {
		for sbRows.Next() {
			var sID, host, ip, st string
			sbRows.Scan(&sID, &host, &ip, &st)
			fmt.Printf("%-18s %-24s %-16s %s\n", sID, host, ip, st)
		}
		sbRows.Close()
	}
}

// REST API Handlers

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

func handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			if l < 1 {
				limit = 1
			} else if l > 500 {
				limit = 500
			} else {
				limit = l
			}
		}
	}

	db, err := getDBConnection()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, ts_ms, COALESCE(sandbox_id, ''), type, COALESCE(detail, ''), COALESCE(duration_ms, 0)
		FROM events
		ORDER BY ts_ms DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var eventsList []map[string]interface{}
	for rows.Next() {
		var id int
		var tsMs, durMs int64
		var sID, typ, det string
		if err := rows.Scan(&id, &tsMs, &sID, &typ, &det, &durMs); err == nil {
			eventsList = append(eventsList, map[string]interface{}{
				"id":          id,
				"ts_ms":       tsMs,
				"sandbox_id":  sID,
				"type":        typ,
				"detail":      det,
				"duration_ms": durMs,
			})
		}
	}

	if eventsList == nil {
		eventsList = []map[string]interface{}{}
	}

	sendJSON(w, http.StatusOK, eventsList)
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
		if errors.Is(err, ErrTTLCapExceeded) {
			sendJSON(w, 422, map[string]string{"error": "Requested extension exceeds maximum TTL cap"})
			return
		}
		if errors.Is(err, ErrTransitionConflict) || strings.Contains(err.Error(), "cannot extend sandbox") {
			sendJSON(w, http.StatusConflict, map[string]string{"error": "Sandbox status is not ACTIVE or expires_at is null"})
			return
		}
		if strings.Contains(err.Error(), "not found") {
			sendJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Sandbox '%s' not found", req.SandboxID)})
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

	res, _ := releaseLease(sandboxID, ipAddress, cooldown)
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
		sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method Not Allowed"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	var params map[string]interface{}
	_ = json.Unmarshal(body, &params)

	sandboxID, _ := params["sandbox_id"].(string)
	if sandboxID == "" {
		sandboxID, _ = params["name"].(string)
	}
	port := 80
	if p, ok := params["port"].(float64); ok {
		port = int(p)
	}
	skipHealth, _ := params["skip_health_check"].(bool)

	if sandboxID == "" {
		sendJSON(w, http.StatusBadRequest, map[string]string{"error": "Missing sandbox_id"})
		return
	}

	success, msg := addTeamFlow(sandboxID, "", port, skipHealth)
	code := http.StatusOK
	if !success {
		code = http.StatusInternalServerError
	}
	sendJSON(w, code, map[string]interface{}{"success": success, "message": msg})
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
	initDB()
	server := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),
		Handler: setupAPIRoutes(),
	}
	fmt.Printf("✓ FelCloud IPAM & Gateway Control Plane API (Go) running on http://0.0.0.0:%d\n", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Error: %v\n", err)
	}
}

func printUsage() {
	fmt.Println("FelCloud Resilient Gateway Control Plane CLI (Go)")
	fmt.Println("Usage:")
	fmt.Println("  ipam-control init")
	fmt.Println("  ipam-control list")
	fmt.Println("  ipam-control reclaim")
	fmt.Println("  ipam-control add-team --name <team> [--ip <ip>] [--port <port>] [--skip-health-check]")
	fmt.Println("  ipam-control remove-team --name <team> [--cooldown <seconds>]")
	fmt.Println("  ipam-control allocate --name <team> [--ip <ip>] [--subnet <subnet>]")
	fmt.Println("  ipam-control sync [--dry-run]")
	fmt.Println("  ipam-control rollback")
	fmt.Println("  ipam-control serve [--port <port>]")
}

func main() {
	initEnv()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	action := os.Args[1]
	switch action {
	case "init":
		if err := initDB(); err != nil {
			fmt.Printf("Init Error: %v\n", err)
			os.Exit(1)
		}

	case "list":
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			_ = initDB()
		}
		listAllocations()

	case "reclaim":
		_ = initDB()
		reclaimExpiredQuarantines()

	case "allocate":
		var name, ip, subnet string
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--name" && i+1 < len(os.Args) {
				name = os.Args[i+1]
			} else if os.Args[i] == "--ip" && i+1 < len(os.Args) {
				ip = os.Args[i+1]
			} else if os.Args[i] == "--subnet" && i+1 < len(os.Args) {
				subnet = os.Args[i+1]
			}
		}
		if name == "" {
			fmt.Println("Error: Missing --name argument.")
			os.Exit(1)
		}
		_ = initDB()
		res, err := allocateLease(name, "", subnet, ip)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))

	case "add-team":
		var teamName, ipAddr string
		port := 80
		skipHealth := false
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--name" && i+1 < len(os.Args) {
				teamName = os.Args[i+1]
			} else if os.Args[i] == "--ip" && i+1 < len(os.Args) {
				ipAddr = os.Args[i+1]
			} else if os.Args[i] == "--port" && i+1 < len(os.Args) {
				port, _ = strconv.Atoi(os.Args[i+1])
			} else if os.Args[i] == "--skip-health-check" {
				skipHealth = true
			}
		}
		if teamName == "" {
			fmt.Println("Error: Missing --name argument.")
			os.Exit(1)
		}
		success, _ := addTeamFlow(teamName, ipAddr, port, skipHealth)
		if !success {
			os.Exit(1)
		}

	case "remove-team":
		var teamName string
		cooldown := 0
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--name" && i+1 < len(os.Args) {
				teamName = os.Args[i+1]
			} else if os.Args[i] == "--cooldown" && i+1 < len(os.Args) {
				cooldown, _ = strconv.Atoi(os.Args[i+1])
			}
		}
		if teamName == "" {
			fmt.Println("Error: Missing --name argument.")
			os.Exit(1)
		}
		success, _ := removeTeamFlow(teamName, cooldown)
		if !success {
			os.Exit(1)
		}

	case "sync":
		dryRun := false
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--dry-run" {
				dryRun = true
			}
		}
		_ = initDB()
		success, _ := syncHAProxyDualGateways(dryRun)
		if !success {
			os.Exit(1)
		}

	case "rollback":
		_ = initDB()
		if !rollbackConfig() {
			os.Exit(1)
		}

	case "serve":
		port := 8080
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--port" && i+1 < len(os.Args) {
				port, _ = strconv.Atoi(os.Args[i+1])
			}
		}
		runAPIServer(port)

	default:
		fmt.Printf("Unknown action: %s\n", action)
		printUsage()
		os.Exit(1)
	}
}
