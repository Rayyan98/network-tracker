package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rewaa/network-tracker/internal/aggregate"
)

const schemaVersion = 3

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

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

		CREATE TABLE IF NOT EXISTS samples_1s (
			ts             INTEGER NOT NULL PRIMARY KEY,
			upload_bps     REAL NOT NULL,
			download_bps   REAL NOT NULL,
			rtt_avg_ms     REAL,
			rtt_p95_ms     REAL,
			rtt_max_ms     REAL,
			jitter_ms      REAL,
			loss_pct       REAL,
			active_flows   INTEGER NOT NULL,
			rtt_count      INTEGER NOT NULL DEFAULT 0,
			segments       INTEGER NOT NULL DEFAULT 0,
			retrans        INTEGER NOT NULL DEFAULT 0,
			udp_upload_bps REAL NOT NULL DEFAULT 0,
			udp_download_bps REAL NOT NULL DEFAULT 0,
			dns_avg_ms     REAL,
			dns_max_ms     REAL,
			dns_count      INTEGER NOT NULL DEFAULT 0,
			quality_score  INTEGER NOT NULL DEFAULT -1
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS samples_1m (
			ts               INTEGER NOT NULL PRIMARY KEY,
			upload_avg       REAL NOT NULL,
			upload_max       REAL NOT NULL,
			download_avg     REAL NOT NULL,
			download_max     REAL NOT NULL,
			rtt_avg          REAL,
			rtt_p95          REAL,
			rtt_max          REAL,
			jitter_avg       REAL,
			loss_avg         REAL,
			loss_max         REAL,
			active_flows_avg REAL NOT NULL,
			sample_count     INTEGER NOT NULL,
			udp_upload_avg   REAL NOT NULL DEFAULT 0,
			udp_download_avg REAL NOT NULL DEFAULT 0,
			dns_avg          REAL,
			quality_avg      REAL
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS samples_1h (
			ts               INTEGER NOT NULL PRIMARY KEY,
			upload_avg       REAL NOT NULL,
			upload_max       REAL NOT NULL,
			download_avg     REAL NOT NULL,
			download_max     REAL NOT NULL,
			rtt_avg          REAL,
			rtt_p95          REAL,
			rtt_max          REAL,
			jitter_avg       REAL,
			loss_avg         REAL,
			loss_max         REAL,
			active_flows_avg REAL NOT NULL,
			sample_count     INTEGER NOT NULL,
			udp_upload_avg   REAL NOT NULL DEFAULT 0,
			udp_download_avg REAL NOT NULL DEFAULT 0,
			dns_avg          REAL,
			quality_avg      REAL
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS samples_1d (
			ts               INTEGER NOT NULL PRIMARY KEY,
			upload_avg       REAL NOT NULL,
			upload_max       REAL NOT NULL,
			download_avg     REAL NOT NULL,
			download_max     REAL NOT NULL,
			rtt_avg          REAL,
			rtt_p95          REAL,
			rtt_max          REAL,
			jitter_avg       REAL,
			loss_avg         REAL,
			loss_max         REAL,
			active_flows_avg REAL NOT NULL,
			sample_count     INTEGER NOT NULL,
			udp_upload_avg   REAL NOT NULL DEFAULT 0,
			udp_download_avg REAL NOT NULL DEFAULT 0,
			dns_avg          REAL,
			quality_avg      REAL
		) WITHOUT ROWID;

		CREATE TABLE IF NOT EXISTS breakdown_1s (
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

		CREATE TABLE IF NOT EXISTS breakdown_1m (
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

		CREATE TABLE IF NOT EXISTS breakdown_1h (
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

		CREATE INDEX IF NOT EXISTS idx_breakdown_1s_kind ON breakdown_1s(kind, ts);
		CREATE INDEX IF NOT EXISTS idx_breakdown_1m_kind ON breakdown_1m(kind, ts);
		CREATE INDEX IF NOT EXISTS idx_breakdown_1h_kind ON breakdown_1h(kind, ts);
	`)
	if err != nil {
		return fmt.Errorf("creating tables: %w", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&count)
	if count == 0 {
		db.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion)
	}
	return nil
}

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
		(ts, upload_bps, download_bps, rtt_avg_ms, rtt_p95_ms, rtt_max_ms, jitter_ms,
		 loss_pct, active_flows, rtt_count, segments, retrans,
		 udp_upload_bps, udp_download_bps, dns_avg_ms, dns_max_ms, dns_count, quality_score)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
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
		var rttAvg, rttP95, rttMax, jitter, lossPct, dnsAvg, dnsMax *float64
		if s.RTTCount > 0 {
			rttAvg = &s.RTTAvgMs
			rttP95 = &s.RTTP95Ms
			rttMax = &s.RTTMaxMs
			jitter = &s.JitterMs
		}
		if s.Segments > 0 {
			lossPct = &s.LossPct
		}
		if s.DNSCount > 0 {
			dnsAvg = &s.DNSAvgMs
			dnsMax = &s.DNSMaxMs
		}
		_, err := stmt.Exec(s.Timestamp, s.UploadBPS, s.DownloadBPS,
			rttAvg, rttP95, rttMax, jitter, lossPct,
			s.ActiveFlows, s.RTTCount, s.Segments, s.Retrans,
			s.UDPUploadBPS, s.UDPDownloadBPS, dnsAvg, dnsMax, s.DNSCount,
			s.QualityScore)
		if err != nil {
			return err
		}

		writeBreakdowns(bdStmt, s.Timestamp, "app", s.ByApp)
		writeBreakdowns(bdStmt, s.Timestamp, "dest", s.ByDest)
	}

	return tx.Commit()
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
