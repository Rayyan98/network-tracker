package server

import (
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/rewaa/network-tracker/internal/resolve"
	"github.com/rewaa/network-tracker/internal/storage"
)

type Server struct {
	store       *storage.DB
	flowCounter func() int
	iface       string
	startTime   time.Time
	addr        string
	webFS       fs.FS
	resolver    *resolve.Resolver
}

func New(store *storage.DB, flowCounter func() int, iface string, addr string, webFS fs.FS, resolver *resolve.Resolver) *Server {
	return &Server{
		store:       store,
		flowCounter: flowCounter,
		iface:       iface,
		startTime:   time.Now(),
		addr:        addr,
		webFS:       webFS,
		resolver:    resolver,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/breakdown", s.handleBreakdown)
	mux.HandleFunc("/api/breakdown/ts", s.handleBreakdownTS)

	mux.Handle("/", http.FileServer(http.FS(s.webFS)))

	log.Printf("Dashboard: http://%s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}
