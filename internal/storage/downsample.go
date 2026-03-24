package storage

import (
	"log"
	"time"
)

// RunDownsampler periodically rolls up data from finer to coarser granularity.
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
			if err := d.downsampleToMinute(); err != nil {
				log.Printf("downsample 1s->1m error: %v", err)
			}
			if err := d.downsampleBreakdown("breakdown_1s", "breakdown_1m", 60, 120); err != nil {
				log.Printf("downsample breakdown 1s->1m error: %v", err)
			}
			d.pruneOld("samples_1s", 3600)
			d.pruneOld("breakdown_1s", 3600)
		case <-hourTicker.C:
			if err := d.downsampleToHour(); err != nil {
				log.Printf("downsample 1m->1h error: %v", err)
			}
			if err := d.downsampleBreakdown("breakdown_1m", "breakdown_1h", 3600, 7200); err != nil {
				log.Printf("downsample breakdown 1m->1h error: %v", err)
			}
			d.pruneOld("samples_1m", 7*24*3600)
			d.pruneOld("breakdown_1m", 7*24*3600)
		case <-dayTicker.C:
			if err := d.downsampleToDay(); err != nil {
				log.Printf("downsample 1h->1d error: %v", err)
			}
			d.pruneOld("samples_1h", 90*24*3600)
			d.pruneOld("breakdown_1h", 90*24*3600)
		case <-done:
			return
		}
	}
}

func (d *DB) downsampleToMinute() error {
	_, err := d.db.Exec(`
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
		WHERE ts >= (strftime('%s', 'now') - 120)
		GROUP BY bucket
	`)
	return err
}

func (d *DB) downsampleToHour() error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO samples_1h
			(ts, upload_avg, upload_max, download_avg, download_max,
			 rtt_avg, rtt_p95, rtt_max, jitter_avg, loss_avg, loss_max,
			 active_flows_avg, sample_count,
			 udp_upload_avg, udp_download_avg, dns_avg, quality_avg)
		SELECT
			(ts / 3600) * 3600 as bucket,
			AVG(upload_avg), MAX(upload_max),
			AVG(download_avg), MAX(download_max),
			AVG(rtt_avg), MAX(rtt_p95), MAX(rtt_max),
			AVG(jitter_avg),
			AVG(loss_avg), MAX(loss_max),
			AVG(active_flows_avg), SUM(sample_count),
			AVG(udp_upload_avg), AVG(udp_download_avg),
			AVG(dns_avg), AVG(quality_avg)
		FROM samples_1m
		WHERE ts >= (strftime('%s', 'now') - 7200)
		GROUP BY bucket
	`)
	return err
}

func (d *DB) downsampleToDay() error {
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO samples_1d
			(ts, upload_avg, upload_max, download_avg, download_max,
			 rtt_avg, rtt_p95, rtt_max, jitter_avg, loss_avg, loss_max,
			 active_flows_avg, sample_count,
			 udp_upload_avg, udp_download_avg, dns_avg, quality_avg)
		SELECT
			(ts / 86400) * 86400 as bucket,
			AVG(upload_avg), MAX(upload_max),
			AVG(download_avg), MAX(download_max),
			AVG(rtt_avg), MAX(rtt_p95), MAX(rtt_max),
			AVG(jitter_avg),
			AVG(loss_avg), MAX(loss_max),
			AVG(active_flows_avg), SUM(sample_count),
			AVG(udp_upload_avg), AVG(udp_download_avg),
			AVG(dns_avg), AVG(quality_avg)
		FROM samples_1h
		WHERE ts >= (strftime('%s', 'now') - 172800)
		GROUP BY bucket
	`)
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
