package storage

import (
	"database/sql"
	"fmt"
)

// MetricRow is the unified response for time-series data.
// Which fields are populated depends on the query.
type MetricRow struct {
	Timestamp      int64   `json:"ts"`
	UploadAvg      float64 `json:"upload_avg"`
	UploadMax      float64 `json:"upload_max"`
	DownloadAvg    float64 `json:"download_avg"`
	DownloadMax    float64 `json:"download_max"`
	UDPUploadAvg   float64 `json:"udp_upload_avg"`
	UDPDownloadAvg float64 `json:"udp_download_avg"`
	TransferDownAvg float64 `json:"transfer_down_avg"`
	TransferUpAvg   float64 `json:"transfer_up_avg"`

	// RTT measures — populated based on requested measure
	RTT     *float64 `json:"rtt"`      // the selected measure
	RTTMin  *float64 `json:"rtt_min"`
	RTTAvg  *float64 `json:"rtt_avg"`
	RTTP50  *float64 `json:"rtt_p50"`
	RTTP90  *float64 `json:"rtt_p90"`
	RTTP95  *float64 `json:"rtt_p95"`
	RTTP99  *float64 `json:"rtt_p99"`
	RTTMax  *float64 `json:"rtt_max"`

	// Jitter
	Jitter    *float64 `json:"jitter"`
	JitterMin *float64 `json:"jitter_min,omitempty"`
	JitterAvg *float64 `json:"jitter_avg,omitempty"`
	JitterP95 *float64 `json:"jitter_p95,omitempty"`
	JitterMax *float64 `json:"jitter_max,omitempty"`

	// Loss
	Loss    *float64 `json:"loss"`
	LossMin *float64 `json:"loss_min,omitempty"`
	LossAvg *float64 `json:"loss_avg,omitempty"`
	LossP95 *float64 `json:"loss_p95,omitempty"`
	LossMax *float64 `json:"loss_max,omitempty"`

	// DNS measures
	DNS    *float64 `json:"dns"`
	DNSMin *float64 `json:"dns_min,omitempty"`
	DNSAvg *float64 `json:"dns_avg,omitempty"`
	DNSP50 *float64 `json:"dns_p50,omitempty"`
	DNSP90 *float64 `json:"dns_p90,omitempty"`
	DNSP95 *float64 `json:"dns_p95,omitempty"`
	DNSP99 *float64 `json:"dns_p99,omitempty"`
	DNSMax *float64 `json:"dns_max,omitempty"`

	QualityAvg  *float64 `json:"quality_avg"`
	ActiveFlows float64  `json:"active_flows"`
	SampleCount int      `json:"sample_count"`
}

// QueryMetrics returns time-series data with all percentile columns.
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
		SELECT ts, upload_bps, download_bps, udp_upload_bps, udp_download_bps,
			transfer_down_bps, transfer_up_bps,
			rtt_min, rtt_avg, rtt_p50, rtt_p90, rtt_p95, rtt_p99, rtt_max,
			jitter, loss_pct,
			dns_min, dns_avg, dns_p50, dns_p90, dns_p95, dns_p99, dns_max,
			quality_score, active_flows
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
		var rttMin, rttAvg, rttP50, rttP90, rttP95, rttP99, rttMax sql.NullFloat64
		var jitter, lossPct sql.NullFloat64
		var dnsMin, dnsAvg, dnsP50, dnsP90, dnsP95, dnsP99, dnsMax sql.NullFloat64
		var qualityScore int

		err := rows.Scan(&r.Timestamp, &r.UploadAvg, &r.DownloadAvg,
			&r.UDPUploadAvg, &r.UDPDownloadAvg,
			&r.TransferDownAvg, &r.TransferUpAvg,
			&rttMin, &rttAvg, &rttP50, &rttP90, &rttP95, &rttP99, &rttMax,
			&jitter, &lossPct,
			&dnsMin, &dnsAvg, &dnsP50, &dnsP90, &dnsP95, &dnsP99, &dnsMax,
			&qualityScore, &r.ActiveFlows)
		if err != nil {
			return nil, err
		}

		r.UploadMax = r.UploadAvg
		r.DownloadMax = r.DownloadAvg
		r.SampleCount = 1

		setNF(&r.RTTMin, rttMin)
		setNF(&r.RTTAvg, rttAvg)
		setNF(&r.RTTP50, rttP50)
		setNF(&r.RTTP90, rttP90)
		setNF(&r.RTTP95, rttP95)
		setNF(&r.RTTP99, rttP99)
		setNF(&r.RTTMax, rttMax)
		r.RTT = r.RTTP95 // default measure

		// Jitter: single value at 1s level, fill all slots with same value
		setNF(&r.Jitter, jitter)
		setNF(&r.JitterAvg, jitter)
		r.JitterMin = r.JitterAvg
		r.JitterP95 = r.JitterAvg
		r.JitterMax = r.JitterAvg

		// Loss: single value at 1s level
		setNF(&r.Loss, lossPct)
		setNF(&r.LossAvg, lossPct)
		r.LossMin = r.LossAvg
		r.LossP95 = r.LossAvg
		r.LossMax = r.LossAvg

		setNF(&r.DNSMin, dnsMin)
		setNF(&r.DNSAvg, dnsAvg)
		setNF(&r.DNSP50, dnsP50)
		setNF(&r.DNSP90, dnsP90)
		setNF(&r.DNSP95, dnsP95)
		setNF(&r.DNSP99, dnsP99)
		setNF(&r.DNSMax, dnsMax)
		r.DNS = r.DNSP95 // default measure

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
			udp_upload_avg, udp_download_avg, transfer_down_avg, transfer_up_avg,
			rtt_min, rtt_avg, rtt_p50, rtt_p90, rtt_p95, rtt_p99, rtt_max,
			jitter_min, jitter_avg, jitter_p95, jitter_max,
			loss_min, loss_avg, loss_p95, loss_max,
			dns_min, dns_avg, dns_p50, dns_p90, dns_p95, dns_p99, dns_max,
			quality_min, quality_avg, quality_max,
			active_flows_avg, sample_count
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
		var rttMin, rttAvg, rttP50, rttP90, rttP95, rttP99, rttMax sql.NullFloat64
		var jitterMin, jitterAvg, jitterP95, jitterMax sql.NullFloat64
		var lossMin, lossAvg, lossP95, lossMax sql.NullFloat64
		var dnsMin, dnsAvg, dnsP50, dnsP90, dnsP95, dnsP99, dnsMax sql.NullFloat64
		var qualityMin, qualityAvg, qualityMax sql.NullFloat64

		err := rows.Scan(&r.Timestamp, &r.UploadAvg, &r.UploadMax,
			&r.DownloadAvg, &r.DownloadMax,
			&r.UDPUploadAvg, &r.UDPDownloadAvg, &r.TransferDownAvg, &r.TransferUpAvg,
			&rttMin, &rttAvg, &rttP50, &rttP90, &rttP95, &rttP99, &rttMax,
			&jitterMin, &jitterAvg, &jitterP95, &jitterMax,
			&lossMin, &lossAvg, &lossP95, &lossMax,
			&dnsMin, &dnsAvg, &dnsP50, &dnsP90, &dnsP95, &dnsP99, &dnsMax,
			&qualityMin, &qualityAvg, &qualityMax,
			&r.ActiveFlows, &r.SampleCount)
		if err != nil {
			return nil, err
		}

		setNF(&r.RTTMin, rttMin)
		setNF(&r.RTTAvg, rttAvg)
		setNF(&r.RTTP50, rttP50)
		setNF(&r.RTTP90, rttP90)
		setNF(&r.RTTP95, rttP95)
		setNF(&r.RTTP99, rttP99)
		setNF(&r.RTTMax, rttMax)
		r.RTT = r.RTTP95

		setNF(&r.JitterMin, jitterMin)
		setNF(&r.JitterAvg, jitterAvg)
		setNF(&r.JitterP95, jitterP95)
		setNF(&r.JitterMax, jitterMax)
		r.Jitter = r.JitterP95

		setNF(&r.LossMin, lossMin)
		setNF(&r.LossAvg, lossAvg)
		setNF(&r.LossP95, lossP95)
		setNF(&r.LossMax, lossMax)
		r.Loss = r.LossAvg

		setNF(&r.DNSMin, dnsMin)
		setNF(&r.DNSAvg, dnsAvg)
		setNF(&r.DNSP50, dnsP50)
		setNF(&r.DNSP90, dnsP90)
		setNF(&r.DNSP95, dnsP95)
		setNF(&r.DNSP99, dnsP99)
		setNF(&r.DNSMax, dnsMax)
		r.DNS = r.DNSP95

		setNF(&r.QualityAvg, qualityAvg)

		results = append(results, r)
	}
	return results, rows.Err()
}

func setNF(dst **float64, src sql.NullFloat64) {
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

// --- Status & Breakdown (unchanged) ---

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

func (d *DB) QueryBreakdown(kind string, granularity string, rangeSec int64) ([]BreakdownRow, error) {
	table, err := breakdownTable(granularity)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT key, SUM(upload), SUM(download), AVG(rtt_avg), AVG(loss_pct), SUM(segments), SUM(retrans)
		FROM %s WHERE kind = ? AND ts >= (strftime('%%s', 'now') - ?)
		GROUP BY key ORDER BY SUM(upload) + SUM(download) DESC LIMIT 50
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
		setNF(&r.RTTAvg, rttAvg)
		setNF(&r.LossPct, lossPct)
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

func (d *DB) QueryBreakdownTimeSeries(kind, key, granularity string, rangeSec int64) ([]BreakdownTimeSeriesRow, error) {
	table, err := breakdownTable(granularity)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		SELECT ts, upload, download, rtt_avg, loss_pct
		FROM %s WHERE kind = ? AND key = ? AND ts >= (strftime('%%s', 'now') - ?)
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
		setNF(&r.RTTAvg, rttAvg)
		setNF(&r.LossPct, lossPct)
		results = append(results, r)
	}
	return results, rows.Err()
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
