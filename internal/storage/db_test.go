package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rewaa/network-tracker/internal/aggregate"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	err = db.RawDB().QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
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

	now := int64(1700000000)
	for i := int64(0); i < 10; i++ {
		db.WriteSample(aggregate.AggregatedSample{
			Timestamp:   now + i,
			UploadBPS:   float64(100 * (i + 1)),
			DownloadBPS: float64(200 * (i + 1)),
			RTTAvgMs:    float64(10 + i),
			RTTP95Ms:    float64(20 + i),
			RTTMaxMs:    float64(30 + i),
			JitterMs:    float64(2 + i),
			LossPct:     float64(i) * 0.1,
			ActiveFlows: 5,
			RTTCount:    3,
			Segments:    10,
			Retrans:     1,
			DNSAvgMs:    15,
			DNSMaxMs:    30,
			DNSCount:    2,
			QualityScore: 75,
		})
	}

	err = db.FlushBuffer()
	if err != nil {
		t.Fatal(err)
	}

	var rowCount int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM samples_1s").Scan(&rowCount)
	if rowCount != 10 {
		t.Errorf("expected 10 rows in samples_1s, got %d", rowCount)
	}

	// Check new columns exist and have values
	var jitter, dnsAvg float64
	var quality int
	db.RawDB().QueryRow("SELECT jitter_ms, dns_avg_ms, quality_score FROM samples_1s LIMIT 1").Scan(&jitter, &dnsAvg, &quality)
	if jitter == 0 {
		t.Error("expected non-zero jitter")
	}
	if dnsAvg == 0 {
		t.Error("expected non-zero dns_avg")
	}
	if quality != 75 {
		t.Errorf("expected quality_score=75, got %d", quality)
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
			Timestamp:    now + i,
			UploadBPS:    100,
			DownloadBPS:  200,
			RTTAvgMs:     25,
			RTTP95Ms:     40,
			RTTMaxMs:     50,
			JitterMs:     5,
			LossPct:      0.5,
			ActiveFlows:  3,
			RTTCount:     1,
			Segments:     5,
			Retrans:      0,
			DNSAvgMs:     10,
			DNSCount:     1,
			QualityScore: 80,
		})
	}
	db.FlushBuffer()

	_, err = db.RawDB().Exec(`
		INSERT OR REPLACE INTO samples_1m
			(ts, upload_avg, upload_max, download_avg, download_max,
			 rtt_avg, rtt_p95, rtt_max, jitter_avg, loss_avg, loss_max,
			 active_flows_avg, sample_count,
			 udp_upload_avg, udp_download_avg, dns_avg, quality_avg)
		SELECT
			(ts / 60) * 60 as bucket,
			AVG(upload_bps), MAX(upload_bps),
			AVG(download_bps), MAX(download_bps),
			AVG(rtt_avg_ms), MAX(rtt_p95_ms), MAX(rtt_max_ms),
			AVG(jitter_ms),
			AVG(loss_pct), MAX(loss_pct),
			AVG(active_flows), COUNT(*),
			AVG(udp_upload_bps), AVG(udp_download_bps),
			AVG(dns_avg_ms),
			AVG(CASE WHEN quality_score >= 0 THEN quality_score END)
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

	var avgUp, qualityAvg float64
	db.RawDB().QueryRow("SELECT upload_avg, quality_avg FROM samples_1m LIMIT 1").Scan(&avgUp, &qualityAvg)
	if avgUp != 100 {
		t.Errorf("expected upload_avg=100, got %.1f", avgUp)
	}
	if qualityAvg != 80 {
		t.Errorf("expected quality_avg=80, got %.1f", qualityAvg)
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
