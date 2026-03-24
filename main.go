package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/rewaa/network-tracker/internal/aggregate"
	"github.com/rewaa/network-tracker/internal/capture"
	"github.com/rewaa/network-tracker/internal/config"
	"github.com/rewaa/network-tracker/internal/flow"
	"github.com/rewaa/network-tracker/internal/procnet"
	"github.com/rewaa/network-tracker/internal/resolve"
	"github.com/rewaa/network-tracker/internal/server"
	"github.com/rewaa/network-tracker/internal/storage"
)

//go:embed web/*
var webFS embed.FS

func main() {
	configPath := flag.String("config", "", "path to config.toml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	log.Printf("Starting network-tracker on interface %s", cfg.Capture.Interface)

	// Open storage
	store, err := storage.Open(cfg.Storage.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	// Create components
	tracker := flow.NewTracker()
	agg := aggregate.NewAggregator(tracker.ActiveFlows, tracker.DrainAllSpeeds)
	procMapper := procnet.NewMapper()
	resolver := resolve.NewResolver()

	engine, err := capture.NewEngine(cfg.Capture, cfg.Filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture engine: %v\n", err)
		os.Exit(1)
	}

	// Channels
	done := make(chan struct{})
	packets := make(chan capture.Packet, 1024)
	samples := make(chan aggregate.AggregatedSample, 64)

	// Start capture
	if err := engine.Start(packets, done); err != nil {
		fmt.Fprintf(os.Stderr, "start capture: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Capturing packets on %s", cfg.Capture.Interface)

	// Start background services
	go procMapper.StartRefresh(done)
	go resolver.StartCleanup(done)

	// Packet processing goroutine
	go func() {
		for {
			select {
			case pkt := <-packets:
				sample := tracker.Process(pkt)
				// Enrich with process name from port mapping
				if sample.LocalPort != 0 {
					sample.Process = procMapper.ProcessForPort(sample.LocalPort)
				}
				agg.Add(sample)
			case <-done:
				return
			}
		}
	}()

	// Aggregator -> storage
	go agg.Run(samples, done)
	go func() {
		for {
			select {
			case s := <-samples:
				store.WriteSample(s)
			case <-done:
				return
			}
		}
	}()

	// Storage writer + downsampler
	go store.RunWriter(done)
	go store.RunDownsampler(done)

	// Flow cleanup
	go tracker.StartCleanup(done)

	// Web server
	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		fmt.Fprintf(os.Stderr, "web fs: %v\n", err)
		os.Exit(1)
	}
	srv := server.New(store, tracker.ActiveFlows, engine.Interface(), cfg.Web.Listen, webContent, resolver)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("web server error: %v", err)
		}
	}()

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %v, shutting down...", sig)
	close(done)
	log.Println("Shutdown complete")
}
