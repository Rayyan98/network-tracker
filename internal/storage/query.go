package storage

import (
	"database/sql"
	"fmt"
)

type MetricRow struct {
	Timestamp      int64    `json:"ts"`
	UploadAvg      float64  `json:"upload_avg"`
	UploadMax      float64  `json:"upload_max"`
	DownloadAvg    float64  `json:"download_avg"`
	DownloadMax    float64  `json:"download_max"`
	RTTAvg         *float64 `json:"rtt_avg"`
	RTTP95         *float64 `json:"rtt_p95"`
	RTTMax         *float64 `json:"rtt_max"`
	JitterAvg      *float64 `json:"jitter_avg"`
	LossAvg        *float64 `json:"loss_avg"`
	LossMax        *float64 `json:"loss_max"`
	ActiveFlows    float64  `json:"active_flows"`
	SampleCount    int      `json:"sample_count"`
	UDPUploadAvg   float64  `json:"udp_upload_avg"`
	UDPDownloadAvg float64  `json:"udp_download_avg"`
	DNSAvg         *float64 `json:"dns_avg"`
	QualityAvg     *float64 `json:"quality_avg"`
}

// QueryMetrics returns time-series data for the given range and granularity.
func (d *DB) QueryMetrics(granularity string, rangeSec int64) ([]MetricRow, error) {
	if granularity == "1s" {
		return d.query1s(rangeSec)
	}
	table, err := tableForGranularity(granularity)
	if err != nil {
		return nil, err
	}
	return d.queryAgg(table, rangeSec)
}

func (d *DB) query1s(rangeSec int64) ([]MetricRow, error) {
	rows, err := d.db.Query(`
		SELECT ts, upload_bps, download_bps,
			rtt_avg_ms, rtt_p95_ms, rtt_max_ms, jitter_ms, loss_pct,
			active_flows, rtt_count, segments, retrans,
			udp_upload_bps, udp_download_bps, dns_avg_ms, quality_score
		FROM samples_1s
		WHERE ts >= (strftime('%s', 'now') - ?)
		ORDER BY ts ASC`, rangeSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MetricRow
	for rows.Next() {
		var r MetricRow
		var rttAvg, rttP95, rttMax, jitter, lossPct, dnsAvg sql.NullFloat64
		var qualityScore int
		var rttCount, segments, retrans int
		err := rows.Scan(&r.Timestamp, &r.UploadAvg, &r.DownloadAvg,
			&rttAvg, &rttP95, &rttMax, &jitter, &lossPct,
			&r.ActiveFlows, &rttCount, &segments, &retrans,
			&r.UDPUploadAvg, &r.UDPDownloadAvg, &dnsAvg, &qualityScore)
		if err != nil {
			return nil, err
		}
		r.UploadMax = r.UploadAvg
		r.DownloadMax = r.DownloadAvg
		r.SampleCount = 1
		setNullFloat(&r.RTTAvg, rttAvg)
		setNullFloat(&r.RTTP95, rttP95)
		setNullFloat(&r.RTTMax, rttMax)
		setNullFloat(&r.JitterAvg, jitter)
		setNullFloat(&r.LossAvg, lossPct)
		r.LossMax = r.LossAvg
		setNullFloat(&r.DNSAvg, dnsAvg)
		if qualityScore >= 0 {
			q := float64(qualityScore)
			r.QualityAvg = &q
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (d *DB) queryAgg(table string, rangeSec int64) ([]MetricRow, error) {
	query := fmt.Sprintf(`
		SELECT ts, upload_avg, upload_max, download_avg, download_max,
			rtt_avg, rtt_p95, rtt_max, jitter_avg, loss_avg, loss_max,
			active_flows_avg, sample_count,
			udp_upload_avg, udp_download_avg, dns_avg, quality_avg
		FROM %s
		WHERE ts >= (strftime('%%s', 'now') - ?)
		ORDER BY ts ASC`, table)

	rows, err := d.db.Query(query, rangeSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MetricRow
	for rows.Next() {
		var r MetricRow
		var rttAvg, rttP95, rttMax, jitter, lossAvg, lossMax, dnsAvg, qualityAvg sql.NullFloat64
		err := rows.Scan(&r.Timestamp, &r.UploadAvg, &r.UploadMax,
			&r.DownloadAvg, &r.DownloadMax,
			&rttAvg, &rttP95, &rttMax, &jitter,
			&lossAvg, &lossMax,
			&r.ActiveFlows, &r.SampleCount,
			&r.UDPUploadAvg, &r.UDPDownloadAvg, &dnsAvg, &qualityAvg)
		if err != nil {
			return nil, err
		}
		setNullFloat(&r.RTTAvg, rttAvg)
		setNullFloat(&r.RTTP95, rttP95)
		setNullFloat(&r.RTTMax, rttMax)
		setNullFloat(&r.JitterAvg, jitter)
		setNullFloat(&r.LossAvg, lossAvg)
		setNullFloat(&r.LossMax, lossMax)
		setNullFloat(&r.DNSAvg, dnsAvg)
		setNullFloat(&r.QualityAvg, qualityAvg)
		results = append(results, r)
	}
	return results, rows.Err()
}

func setNullFloat(dst **float64, src sql.NullFloat64) {
	if src.Valid {
		*dst = &src.Float64
	}
}

func tableForGranularity(g string) (string, error) {
	switch g {
	case "1s":
		return "samples_1s", nil
	case "1m":
		return "samples_1m", nil
	case "1h":
		return "samples_1h", nil
	case "1d":
		return "samples_1d", nil
	default:
		return "", fmt.Errorf("invalid granularity: %s", g)
	}
}

// Status returns basic info about the database.
type Status struct {
	Rows1s int `json:"rows_1s"`
	Rows1m int `json:"rows_1m"`
	Rows1h int `json:"rows_1h"`
	Rows1d int `json:"rows_1d"`
}

func (d *DB) Status() Status {
	var s Status
	d.db.QueryRow("SELECT COUNT(*) FROM samples_1s").Scan(&s.Rows1s)
	d.db.QueryRow("SELECT COUNT(*) FROM samples_1m").Scan(&s.Rows1m)
	d.db.QueryRow("SELECT COUNT(*) FROM samples_1h").Scan(&s.Rows1h)
	d.db.QueryRow("SELECT COUNT(*) FROM samples_1d").Scan(&s.Rows1d)
	return s
}

// BreakdownRow represents aggregated metrics for one key (app or destination).
type BreakdownRow struct {
	Key      string   `json:"key"`
	Hostname string   `json:"hostname,omitempty"`
	Upload   float64  `json:"upload"`
	Download float64  `json:"download"`
	RTTAvg   *float64 `json:"rtt_avg"`
	LossPct  *float64 `json:"loss_pct"`
	Segments int      `json:"segments"`
	Retrans  int      `json:"retrans"`
}

// QueryBreakdown returns aggregated breakdown by kind over a time range.
func (d *DB) QueryBreakdown(kind string, granularity string, rangeSec int64) ([]BreakdownRow, error) {
	table, err := breakdownTable(granularity)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT key, SUM(upload), SUM(download),
			AVG(rtt_avg), AVG(loss_pct),
			SUM(segments), SUM(retrans)
		FROM %s
		WHERE kind = ? AND ts >= (strftime('%%s', 'now') - ?)
		GROUP BY key
		ORDER BY SUM(upload) + SUM(download) DESC
		LIMIT 50
	`, table)

	rows, err := d.db.Query(query, kind, rangeSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BreakdownRow
	for rows.Next() {
		var r BreakdownRow
		var rttAvg, lossPct sql.NullFloat64
		err := rows.Scan(&r.Key, &r.Upload, &r.Download, &rttAvg, &lossPct, &r.Segments, &r.Retrans)
		if err != nil {
			return nil, err
		}
		setNullFloat(&r.RTTAvg, rttAvg)
		setNullFloat(&r.LossPct, lossPct)
		results = append(results, r)
	}
	return results, rows.Err()
}

// QueryBreakdownTimeSeries returns time-series data for a specific key.
func (d *DB) QueryBreakdownTimeSeries(kind, key, granularity string, rangeSec int64) ([]BreakdownTimeSeriesRow, error) {
	table, err := breakdownTable(granularity)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT ts, upload, download, rtt_avg, loss_pct
		FROM %s
		WHERE kind = ? AND key = ? AND ts >= (strftime('%%s', 'now') - ?)
		ORDER BY ts ASC
	`, table)

	rows, err := d.db.Query(query, kind, key, rangeSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BreakdownTimeSeriesRow
	for rows.Next() {
		var r BreakdownTimeSeriesRow
		var rttAvg, lossPct sql.NullFloat64
		err := rows.Scan(&r.Timestamp, &r.Upload, &r.Download, &rttAvg, &lossPct)
		if err != nil {
			return nil, err
		}
		setNullFloat(&r.RTTAvg, rttAvg)
		setNullFloat(&r.LossPct, lossPct)
		results = append(results, r)
	}
	return results, rows.Err()
}

type BreakdownTimeSeriesRow struct {
	Timestamp int64    `json:"ts"`
	Upload    float64  `json:"upload"`
	Download  float64  `json:"download"`
	RTTAvg    *float64 `json:"rtt_avg"`
	LossPct   *float64 `json:"loss_pct"`
}

func breakdownTable(g string) (string, error) {
	switch g {
	case "1s":
		return "breakdown_1s", nil
	case "1m":
		return "breakdown_1m", nil
	case "1h":
		return "breakdown_1h", nil
	default:
		return "", fmt.Errorf("invalid granularity: %s", g)
	}
}
