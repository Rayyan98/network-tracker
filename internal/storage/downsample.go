package storage

import (
	"log"
	"time"
)

func (d *DB) RunDownsampler(done <-chan struct{}) {
	minuteTicker := time.NewTicker(1 * time.Minute)
	hourTicker := time.NewTicker(1 * time.Hour)
	dayTicker := time.NewTicker(24 * time.Hour)
	defer minuteTicker.Stop()
	defer hourTicker.Stop()
	defer dayTicker.Stop()

	for {
		select {
		case <-minuteTicker.C:
			if err := d.downsample1sTo1m(); err != nil {
				log.Printf("downsample 1s->1m error: %v", err)
			}
			if err := d.downsampleBreakdown("breakdown_1s", "breakdown_1m", 60, 120); err != nil {
				log.Printf("downsample breakdown 1s->1m error: %v", err)
			}
			d.pruneOld("samples_1s", 3600)
			d.pruneOld("breakdown_1s", 3600)
		case <-hourTicker.C:
			if err := d.downsampleAgg("samples_1m", "samples_1h", 3600, 7200); err != nil {
				log.Printf("downsample 1m->1h error: %v", err)
			}
			if err := d.downsampleBreakdown("breakdown_1m", "breakdown_1h", 3600, 7200); err != nil {
				log.Printf("downsample breakdown 1m->1h error: %v", err)
			}
			d.pruneOld("samples_1m", 7*24*3600)
			d.pruneOld("breakdown_1m", 7*24*3600)
		case <-dayTicker.C:
			if err := d.downsampleAgg("samples_1h", "samples_1d", 86400, 172800); err != nil {
				log.Printf("downsample 1h->1d error: %v", err)
			}
			d.pruneOld("samples_1h", 90*24*3600)
			d.pruneOld("breakdown_1h", 90*24*3600)
		case <-done:
			return
		}
	}
}

// downsample1sTo1m aggregates 1-second samples into 1-minute buckets.
func (d *DB) downsample1sTo1m() error {
	_, err := d.db.Exec(`
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
			AVG(CASE WHEN transfer_down_bps > 0 THEN transfer_down_bps END),
			AVG(CASE WHEN transfer_up_bps > 0 THEN transfer_up_bps END),
			-- RTT: aggregate per-second percentiles
			MIN(rtt_min), AVG(rtt_avg), AVG(rtt_p50), AVG(rtt_p90),
			AVG(rtt_p95), AVG(rtt_p99), MAX(rtt_max),
			-- Jitter: aggregate per-second jitter values
			MIN(jitter), AVG(jitter), MAX(jitter), MAX(jitter),
			-- Loss: aggregate per-second loss rates
			MIN(loss_pct), AVG(loss_pct), MAX(loss_pct), MAX(loss_pct),
			-- DNS: aggregate per-second percentiles
			MIN(dns_min), AVG(dns_avg), AVG(dns_p50), AVG(dns_p90),
			AVG(dns_p95), AVG(dns_p99), MAX(dns_max),
			-- Quality
			MIN(CASE WHEN quality_score >= 0 THEN quality_score END),
			AVG(CASE WHEN quality_score >= 0 THEN quality_score END),
			MAX(CASE WHEN quality_score >= 0 THEN quality_score END),
			AVG(active_flows), COUNT(*)
		FROM samples_1s
		WHERE ts >= (strftime('%s', 'now') - 120)
		GROUP BY bucket
	`)
	return err
}

// downsampleAgg aggregates from one aggregated table to the next coarser one.
// Both source and dest have the same column structure.
func (d *DB) downsampleAgg(src, dst string, bucketSec, lookbackSec int64) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO `+dst+`
			(ts, upload_avg, upload_max, download_avg, download_max,
			 udp_upload_avg, udp_download_avg, transfer_down_avg, transfer_up_avg,
			 rtt_min, rtt_avg, rtt_p50, rtt_p90, rtt_p95, rtt_p99, rtt_max,
			 jitter_min, jitter_avg, jitter_p95, jitter_max,
			 loss_min, loss_avg, loss_p95, loss_max,
			 dns_min, dns_avg, dns_p50, dns_p90, dns_p95, dns_p99, dns_max,
			 quality_min, quality_avg, quality_max,
			 active_flows_avg, sample_count)
		SELECT
			(ts / ?) * ? as bucket,
			AVG(upload_avg), MAX(upload_max),
			AVG(download_avg), MAX(download_max),
			AVG(udp_upload_avg), AVG(udp_download_avg),
			AVG(transfer_down_avg), AVG(transfer_up_avg),
			MIN(rtt_min), AVG(rtt_avg), AVG(rtt_p50), AVG(rtt_p90),
			AVG(rtt_p95), AVG(rtt_p99), MAX(rtt_max),
			MIN(jitter_min), AVG(jitter_avg), AVG(jitter_p95), MAX(jitter_max),
			MIN(loss_min), AVG(loss_avg), AVG(loss_p95), MAX(loss_max),
			MIN(dns_min), AVG(dns_avg), AVG(dns_p50), AVG(dns_p90),
			AVG(dns_p95), AVG(dns_p99), MAX(dns_max),
			MIN(quality_min), AVG(quality_avg), MAX(quality_max),
			AVG(active_flows_avg), SUM(sample_count)
		FROM `+src+`
		WHERE ts >= (strftime('%s', 'now') - ?)
		GROUP BY bucket
	`, bucketSec, bucketSec, lookbackSec)
	return err
}

func (d *DB) downsampleBreakdown(srcTable, dstTable string, bucketSec, lookbackSec int64) error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO `+dstTable+` (ts, kind, key, upload, download, rtt_avg, loss_pct, segments, retrans)
		SELECT
			(ts / ?) * ? as bucket,
			kind, key,
			AVG(upload), AVG(download),
			AVG(rtt_avg), AVG(loss_pct),
			SUM(segments), SUM(retrans)
		FROM `+srcTable+`
		WHERE ts >= (strftime('%s', 'now') - ?)
		GROUP BY bucket, kind, key
	`, bucketSec, bucketSec, lookbackSec)
	return err
}

func (d *DB) pruneOld(table string, maxAgeSec int64) error {
	_, err := d.db.Exec(
		"DELETE FROM "+table+" WHERE ts < (strftime('%s', 'now') - ?)",
		maxAgeSec,
	)
	return err
}
