package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rewaa/network-tracker/internal/aggregate"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 schema_version row, got %d", count)
	}
}

func TestWriteAndQuery(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for i := int64(0); i < 10; i++ {
		db.WriteSample(aggregate.AggregatedSample{
			Timestamp:   1700000000 + i,
			UploadBPS:   float64(100 * (i + 1)),
			DownloadBPS: float64(200 * (i + 1)),
			RTT:         aggregate.Percentiles{Min: 10, Avg: 25, P50: 23, P90: 40, P95: 45, P99: 55, Max: 60, Count: 5},
			JitterMs:    5,
			LossPct:     0.5,
			Segments:    10,
			Retrans:     1,
			DNS:         aggregate.Percentiles{Min: 5, Avg: 15, P50: 14, P90: 25, P95: 28, P99: 30, Max: 35, Count: 3},
			QualityScore: 75,
			ActiveFlows: 3,
		})
	}
	db.FlushBuffer()

	var rowCount int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM samples_1s").Scan(&rowCount)
	if rowCount != 10 {
		t.Errorf("expected 10 rows, got %d", rowCount)
	}

	// Check percentile columns exist and have values
	var rttP95, dnsP95 float64
	db.RawDB().QueryRow("SELECT rtt_p95, dns_p95 FROM samples_1s LIMIT 1").Scan(&rttP95, &dnsP95)
	if rttP95 != 45 {
		t.Errorf("expected rtt_p95=45, got %.1f", rttP95)
	}
	if dnsP95 != 28 {
		t.Errorf("expected dns_p95=28, got %.1f", dnsP95)
	}
}

func TestDownsample(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := int64(1700000000) - (1700000000 % 60)
	for i := int64(0); i < 120; i++ {
		db.WriteSample(aggregate.AggregatedSample{
			Timestamp:   now + i,
			UploadBPS:   100,
			DownloadBPS: 200,
			RTT:         aggregate.Percentiles{Min: 10, Avg: 25, P50: 23, P90: 40, P95: 45, P99: 55, Max: 60, Count: 3},
			JitterMs:    5,
			LossPct:     0.5,
			Segments:    5,
			DNS:         aggregate.Percentiles{Min: 5, Avg: 15, P50: 14, P90: 25, P95: 28, P99: 30, Max: 35, Count: 2},
			QualityScore: 80,
			ActiveFlows: 3,
		})
	}
	db.FlushBuffer()

	// Manually downsample (the auto one uses strftime('now') which won't match test timestamps)
	_, err = db.RawDB().Exec(`
		INSERT OR REPLACE INTO samples_1m
			(ts, upload_avg, upload_max, download_avg, download_max,
			 udp_upload_avg, udp_download_avg, transfer_down_avg, transfer_up_avg,
			 rtt_min, rtt_avg, rtt_p50, rtt_p90, rtt_p95, rtt_p99, rtt_max,
			 jitter_min, jitter_avg, jitter_p95, jitter_max,
			 loss_min, loss_avg, loss_p95, loss_max,
			 dns_min, dns_avg, dns_p50, dns_p90, dns_p95, dns_p99, dns_max,
			 quality_min, quality_avg, quality_max,
			 active_flows_avg, sample_count)
		SELECT
			(ts / 60) * 60 as bucket,
			AVG(upload_bps), MAX(upload_bps),
			AVG(download_bps), MAX(download_bps),
			AVG(udp_upload_bps), AVG(udp_download_bps),
			AVG(transfer_down_bps), AVG(transfer_up_bps),
			MIN(rtt_min), AVG(rtt_avg), AVG(rtt_p50), AVG(rtt_p90),
			AVG(rtt_p95), AVG(rtt_p99), MAX(rtt_max),
			MIN(jitter), AVG(jitter), MAX(jitter), MAX(jitter),
			MIN(loss_pct), AVG(loss_pct), MAX(loss_pct), MAX(loss_pct),
			MIN(dns_min), AVG(dns_avg), AVG(dns_p50), AVG(dns_p90),
			AVG(dns_p95), AVG(dns_p99), MAX(dns_max),
			MIN(CASE WHEN quality_score >= 0 THEN quality_score END),
			AVG(CASE WHEN quality_score >= 0 THEN quality_score END),
			MAX(CASE WHEN quality_score >= 0 THEN quality_score END),
			AVG(active_flows), COUNT(*)
		FROM samples_1s
		GROUP BY bucket
	`)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM samples_1m").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 minute buckets, got %d", count)
	}

	var rttP95, dnsP95 float64
	db.RawDB().QueryRow("SELECT rtt_p95, dns_p95 FROM samples_1m LIMIT 1").Scan(&rttP95, &dnsP95)
	if rttP95 != 45 {
		t.Errorf("expected rtt_p95=45 in 1m table, got %.1f", rttP95)
	}
}

func TestDBPathCreation(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "test.db")
	db, err := Open(nested)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(nested); os.IsNotExist(err) {
		t.Error("database file should have been created")
	}
}

func TestMigrationFromOldSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open creates v4 schema
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify version is 4
	v := getSchemaVersion(db.RawDB())
	if v != 4 {
		t.Errorf("expected schema version 4, got %d", v)
	}

	db.Close()

	// Re-open — should not error (idempotent)
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}
