package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rewaa/network-tracker/internal/storage"
)

type statusResponse struct {
	Uptime      string         `json:"uptime"`
	ActiveFlows int            `json:"active_flows"`
	Interface   string         `json:"interface"`
	DB          storage.Status `json:"db"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{
		Uptime:      time.Since(s.startTime).Round(time.Second).String(),
		ActiveFlows: s.flowCounter(),
		Interface:   s.iface,
		DB:          s.store.Status(),
	}
	writeJSON(w, resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	rangeStr := r.URL.Query().Get("range")
	granularity := r.URL.Query().Get("granularity")

	if granularity == "" {
		granularity = "1s"
	}

	rangeSec := parseRange(rangeStr)
	if rangeSec == 0 {
		rangeSec = 3600 // default 1 hour
	}

	// Auto-select granularity if not specified or if "auto"
	if r.URL.Query().Get("granularity") == "" || r.URL.Query().Get("granularity") == "auto" {
		granularity = autoGranularity(rangeSec)
	}

	rows, err := s.store.QueryMetrics(granularity, rangeSec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if rows == nil {
		rows = []storage.MetricRow{}
	}

	writeJSON(w, map[string]interface{}{
		"granularity": granularity,
		"range_sec":   rangeSec,
		"data":        rows,
	})
}

func autoGranularity(rangeSec int64) string {
	switch {
	case rangeSec <= 3600: // 1h
		return "1s"
	case rangeSec <= 7*86400: // 7d
		return "1m"
	case rangeSec <= 90*86400: // 90d
		return "1h"
	default:
		return "1d"
	}
}

func parseRange(s string) int64 {
	if s == "" {
		return 0
	}
	// Try parsing as seconds first
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
		return sec
	}
	// Parse human-readable: "1h", "24h", "7d", "30d", "90d"
	if len(s) < 2 {
		return 0
	}
	num, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil {
		return 0
	}
	switch s[len(s)-1] {
	case 's':
		return num
	case 'm':
		return num * 60
	case 'h':
		return num * 3600
	case 'd':
		return num * 86400
	}
	return 0
}

// handleBreakdown returns top apps or destinations with aggregated metrics.
// GET /api/breakdown?kind=app|dest&range=1h
func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "app" && kind != "dest" {
		http.Error(w, "kind must be 'app' or 'dest'", http.StatusBadRequest)
		return
	}

	rangeSec := parseRange(r.URL.Query().Get("range"))
	if rangeSec == 0 {
		rangeSec = 3600
	}

	granularity := autoGranularity(rangeSec)

	rows, err := s.store.QueryBreakdown(kind, granularity, rangeSec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Resolve hostnames for destinations
	if kind == "dest" && s.resolver != nil {
		for i := range rows {
			rows[i].Hostname = s.resolver.Lookup(rows[i].Key)
		}
	}

	if rows == nil {
		rows = []storage.BreakdownRow{}
	}

	writeJSON(w, map[string]interface{}{
		"kind":        kind,
		"granularity": granularity,
		"range_sec":   rangeSec,
		"data":        rows,
	})
}

// handleBreakdownTS returns time-series for a specific app or destination.
// GET /api/breakdown/ts?kind=app&key=Safari&range=1h
func (s *Server) handleBreakdownTS(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	key := r.URL.Query().Get("key")
	if kind == "" || key == "" {
		http.Error(w, "kind and key are required", http.StatusBadRequest)
		return
	}

	rangeSec := parseRange(r.URL.Query().Get("range"))
	if rangeSec == 0 {
		rangeSec = 3600
	}

	granularity := autoGranularity(rangeSec)

	rows, err := s.store.QueryBreakdownTimeSeries(kind, key, granularity, rangeSec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if rows == nil {
		rows = []storage.BreakdownTimeSeriesRow{}
	}

	writeJSON(w, map[string]interface{}{
		"kind":        kind,
		"key":         key,
		"granularity": granularity,
		"range_sec":   rangeSec,
		"data":        rows,
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
