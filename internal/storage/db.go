package storage

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rewaa/network-tracker/internal/aggregate"
)

type DB struct {
	db     *sql.DB
	mu     sync.Mutex
	buffer []aggregate.AggregatedSample
}

func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

// --- Schema Migration ---

func getSchemaVersion(db *sql.DB) int {
	var v int
	err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v)
	if err != nil {
		return 0
	}
	return v
}

func setSchemaVersion(db *sql.DB, v int) {
	db.Exec("DELETE FROM schema_version")
	db.Exec("INSERT INTO schema_version (version) VALUES (?)", v)
}

func migrate(db *sql.DB) error {
	// Ensure schema_version table exists
	db.Exec("CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)")

	version := getSchemaVersion(db)

	if version < 4 {
		// Fresh install or upgrading from old version — recreate all tables
		// Drop old tables if they exist (clean slate for major schema change)
		for _, t := range []string{"samples_1s", "samples_1m", "samples_1h", "samples_1d",
			"breakdown_1s", "breakdown_1m", "breakdown_1h"} {
			db.Exec("DROP TABLE IF EXISTS " + t)
		}

		if err := createTablesV4(db); err != nil {
			return err
		}
		setSchemaVersion(db, 4)
		log.Println("Database schema created (v4)")
	}

	return nil
}

func createTablesV4(db *sql.DB) error {
	// 1-second samples: per-second metrics with full percentile breakdowns
	// RTT and DNS have multiple percentiles computed from N samples within that second
	// Jitter and loss are single values per second
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS samples_1s (
			ts               INTEGER NOT NULL PRIMARY KEY,
			-- Throughput
			upload_bps       REAL NOT NULL,
			download_bps     REAL NOT NULL,
			udp_upload_bps   REAL NOT NULL DEFAULT 0,
			udp_download_bps REAL NOT NULL DEFAULT 0,
			-- Transfer speed (link capacity from burst measurement)
			transfer_down_bps REAL NOT NULL DEFAULT 0,
			transfer_up_bps   REAL NOT NULL DEFAULT 0,
			-- RTT percentiles (from N RTT samples in this second)
			rtt_min          REAL,
			rtt_avg          REAL,
			rtt_p50          REAL,
			rtt_p90          REAL,
			rtt_p95          REAL,
			rtt_p99          REAL,
			rtt_max          REAL,
			rtt_count        INTEGER NOT NULL DEFAULT 0,
			-- Jitter (stddev of RTT samples, single value per second)
			jitter           REAL,
			-- Packet loss (rate for this second)
			loss_pct         REAL,
			segments         INTEGER NOT NULL DEFAULT 0,
			retrans          INTEGER NOT NULL DEFAULT 0,
			-- DNS percentiles (from N DNS samples in this second)
			dns_min          REAL,
			dns_avg          REAL,
			dns_p50          REAL,
			dns_p90          REAL,
			dns_p95          REAL,
			dns_p99          REAL,
			dns_max          REAL,
			dns_count        INTEGER NOT NULL DEFAULT 0,
			-- Quality
			quality_score    INTEGER NOT NULL DEFAULT -1,
			active_flows     INTEGER NOT NULL
		) WITHOUT ROWID;
	`)
	if err != nil {
		return fmt.Errorf("creating samples_1s: %w", err)
	}

	// Aggregated tables: store the same percentile set.
	// Values are computed from the finer-granularity table:
	//   min = MIN(min), avg = AVG(avg), p50 = AVG(p50), etc., max = MAX(max)
	for _, table := range []string{"samples_1m", "samples_1h", "samples_1d"} {
		_, err := db.Exec(fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				ts               INTEGER NOT NULL PRIMARY KEY,
				-- Throughput
				upload_avg       REAL NOT NULL,
				upload_max       REAL NOT NULL,
				download_avg     REAL NOT NULL,
				download_max     REAL NOT NULL,
				udp_upload_avg   REAL NOT NULL DEFAULT 0,
				udp_download_avg REAL NOT NULL DEFAULT 0,
				-- Transfer speed
				transfer_down_avg REAL NOT NULL DEFAULT 0,
				transfer_up_avg   REAL NOT NULL DEFAULT 0,
				-- RTT percentiles (aggregated from finer table)
				rtt_min          REAL,
				rtt_avg          REAL,
				rtt_p50          REAL,
				rtt_p90          REAL,
				rtt_p95          REAL,
				rtt_p99          REAL,
				rtt_max          REAL,
				-- Jitter (aggregated from per-second jitter values)
				jitter_min       REAL,
				jitter_avg       REAL,
				jitter_p95       REAL,
				jitter_max       REAL,
				-- Loss (aggregated from per-second loss rates)
				loss_min         REAL,
				loss_avg         REAL,
				loss_p95         REAL,
				loss_max         REAL,
				-- DNS percentiles
				dns_min          REAL,
				dns_avg          REAL,
				dns_p50          REAL,
				dns_p90          REAL,
				dns_p95          REAL,
				dns_p99          REAL,
				dns_max          REAL,
				-- Quality
				quality_min      REAL,
				quality_avg      REAL,
				quality_max      REAL,
				-- Meta
				active_flows_avg REAL NOT NULL,
				sample_count     INTEGER NOT NULL
			) WITHOUT ROWID;
		`, table))
		if err != nil {
			return fmt.Errorf("creating %s: %w", table, err)
		}
	}

	// Breakdown tables (per-app, per-destination)
	for _, table := range []string{"breakdown_1s", "breakdown_1m", "breakdown_1h"} {
		_, err := db.Exec(fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				ts        INTEGER NOT NULL,
				kind      TEXT NOT NULL,
				key       TEXT NOT NULL,
				upload    REAL NOT NULL,
				download  REAL NOT NULL,
				rtt_avg   REAL,
				loss_pct  REAL,
				segments  INTEGER NOT NULL DEFAULT 0,
				retrans   INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (ts, kind, key)
			) WITHOUT ROWID;
			CREATE INDEX IF NOT EXISTS idx_%s_kind ON %s(kind, ts);
		`, table, table, table))
		if err != nil {
			return fmt.Errorf("creating %s: %w", table, err)
		}
	}

	return nil
}

// --- Write ---

// WriteSample buffers a sample for batch writing.
func (d *DB) WriteSample(s aggregate.AggregatedSample) {
	d.mu.Lock()
	d.buffer = append(d.buffer, s)
	d.mu.Unlock()
}

// FlushBuffer writes all buffered samples to the database in a single transaction.
func (d *DB) FlushBuffer() error {
	d.mu.Lock()
	samples := d.buffer
	d.buffer = nil
	d.mu.Unlock()

	if len(samples) == 0 {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO samples_1s
		(ts, upload_bps, download_bps, udp_upload_bps, udp_download_bps,
		 transfer_down_bps, transfer_up_bps,
		 rtt_min, rtt_avg, rtt_p50, rtt_p90, rtt_p95, rtt_p99, rtt_max, rtt_count,
		 jitter, loss_pct, segments, retrans,
		 dns_min, dns_avg, dns_p50, dns_p90, dns_p95, dns_p99, dns_max, dns_count,
		 quality_score, active_flows)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	bdStmt, err := tx.Prepare(`INSERT OR REPLACE INTO breakdown_1s
		(ts, kind, key, upload, download, rtt_avg, loss_pct, segments, retrans)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer bdStmt.Close()

	for _, s := range samples {
		_, err := stmt.Exec(s.Timestamp,
			s.UploadBPS, s.DownloadBPS, s.UDPUploadBPS, s.UDPDownloadBPS,
			s.TransferDownBPS, s.TransferUpBPS,
			nilIfZeroCount(s.RTT.Min, s.RTT.Count), nilIfZeroCount(s.RTT.Avg, s.RTT.Count),
			nilIfZeroCount(s.RTT.P50, s.RTT.Count), nilIfZeroCount(s.RTT.P90, s.RTT.Count),
			nilIfZeroCount(s.RTT.P95, s.RTT.Count), nilIfZeroCount(s.RTT.P99, s.RTT.Count),
			nilIfZeroCount(s.RTT.Max, s.RTT.Count), s.RTT.Count,
			nilIfZeroCount(s.JitterMs, s.RTT.Count),
			nilIfZeroSegments(s.LossPct, s.Segments), s.Segments, s.Retrans,
			nilIfZeroCount(s.DNS.Min, s.DNS.Count), nilIfZeroCount(s.DNS.Avg, s.DNS.Count),
			nilIfZeroCount(s.DNS.P50, s.DNS.Count), nilIfZeroCount(s.DNS.P90, s.DNS.Count),
			nilIfZeroCount(s.DNS.P95, s.DNS.Count), nilIfZeroCount(s.DNS.P99, s.DNS.Count),
			nilIfZeroCount(s.DNS.Max, s.DNS.Count), s.DNS.Count,
			s.QualityScore, s.ActiveFlows)
		if err != nil {
			return err
		}

		writeBreakdowns(bdStmt, s.Timestamp, "app", s.ByApp)
		writeBreakdowns(bdStmt, s.Timestamp, "dest", s.ByDest)
	}

	return tx.Commit()
}

func nilIfZeroCount(val float64, count int) *float64 {
	if count == 0 {
		return nil
	}
	return &val
}

func nilIfZeroSegments(val float64, segments int) *float64 {
	if segments == 0 {
		return nil
	}
	return &val
}

// RunWriter periodically flushes the buffer to disk.
func (d *DB) RunWriter(done <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.FlushBuffer()
		case <-done:
			d.FlushBuffer()
			return
		}
	}
}

func (d *DB) Close() error {
	return d.db.Close()
}

// RawDB exposes the underlying sql.DB for queries.
func (d *DB) RawDB() *sql.DB {
	return d.db
}

func writeBreakdowns(stmt *sql.Stmt, ts int64, kind string, breakdowns map[string]*aggregate.Breakdown) {
	for key, b := range breakdowns {
		var rttAvg, lossPct *float64
		if len(b.RTTSamples) > 0 {
			sum := 0.0
			for _, v := range b.RTTSamples {
				sum += v
			}
			avg := sum / float64(len(b.RTTSamples))
			rttAvg = &avg
		}
		if b.Segments > 0 {
			l := float64(b.Retrans) / float64(b.Segments) * 100
			lossPct = &l
		}
		stmt.Exec(ts, kind, key, b.UploadBytes, b.DownloadBytes, rttAvg, lossPct, b.Segments, b.Retrans)
	}
}
