# Network Tracker

A passive network quality monitor for macOS. Captures real application traffic via libpcap to derive speed, latency, jitter, packet loss, and DNS resolution time — no synthetic pings.

## What It Does

- **Passively monitors** all TCP and UDP traffic on your network interface
- **Derives metrics** from real app traffic (not synthetic probes):
  - Download/upload throughput (TCP + UDP separately)
  - Latency (RTT from TCP handshake and data/ACK timing)
  - Jitter (RTT variance — the metric that determines if calls will be choppy)
  - Packet loss (inferred from TCP retransmissions)
  - DNS resolution time (passive query/response matching)
- **Computes a quality score** (0–100) weighted across all metrics
- **Shows use-case readiness** — can your connection handle HD video calls, game streaming, 4K, etc.
- **Breaks down by application** — see which apps are using bandwidth and their individual quality metrics (uses macOS `libproc`)
- **Breaks down by destination** — see per-server/per-service metrics with reverse DNS
- **Stores weeks/months of history** in SQLite with automatic downsampling (1s → 1m → 1h → 1d)
- **Filters out local traffic** — ignores Docker bridge networks, loopback, and container-to-container traffic

## Dashboard

Web dashboard at `http://127.0.0.1:8787` with:

- Quality score with color-coded bands (green/yellow/red)
- Use-case readiness cards (HD Video Call, Audio Call, Screen Sharing, Game Streaming, 4K Streaming)
- Download/upload speed charts (TCP + UDP split)
- Latency, jitter, packet loss, DNS charts with threshold reference lines
- Per-application and per-destination breakdown tabs with drill-down time series
- Time range selector: 1H, 24H, 7D, 30D, 90D

## Requirements

- macOS (uses `libproc` for process-to-port mapping)
- Go 1.21+ (uses CGO for libpcap and SQLite)
- libpcap (included in macOS SDK)
- `sudo` or BPF device access (required for packet capture)

## Build & Run

```bash
make build
sudo ./bin/network-tracker
```

Then open http://127.0.0.1:8787

### With config file

```bash
cp config.toml.example config.toml
# edit config.toml
sudo ./bin/network-tracker --config config.toml
```

## Configuration

See `config.toml.example`:

```toml
[capture]
interface = "en0"        # network interface
snap_len = 96            # bytes to capture per packet (headers only)
promiscuous = false

[filter]
exclude_networks = ["127.0.0.0/8", "169.254.0.0/16", "172.17.0.0/16", "172.18.0.0/16"]
local_networks = ["10.0.0.0/8", "192.168.0.0/16"]

[storage]
db_path = "~/.network-tracker/data.db"

[web]
listen = "127.0.0.1:8787"
```

## Quality Score

Composite 0–100 score weighted across:

| Component | Weight | Great | OK | Bad |
|---|---|---|---|---|
| Download speed | 20% | >25 MB/s | 5–25 | <5 |
| Upload speed | 15% | >10 MB/s | 2–10 | <2 |
| Latency | 20% | <30ms | 30–100ms | >100ms |
| Jitter | 20% | <10ms | 10–30ms | >30ms |
| Packet loss | 15% | <0.1% | 0.1–1% | >1% |
| Consistency | 10% | stable | some drops | unstable |

## Architecture

```
libpcap (BPF) → Capture Engine → Flow Tracker → Aggregator → SQLite → Web Dashboard
                                  ↕                ↕
                              DNS Tracker    Process Mapper (libproc)
```

All components run as goroutines connected by channels. Single binary, no external dependencies at runtime.

## How Metrics Are Derived

- **Latency**: TCP handshake RTT (SYN → SYN-ACK timing) and data/ACK RTT with Karn's algorithm
- **Packet loss**: TCP retransmission detection (sequence number analysis) with duplicate ACK tracking
- **Jitter**: Standard deviation of RTT samples per second
- **DNS latency**: Passive matching of UDP port 53 query/response pairs
- **Throughput**: Computed from IP header total length field (accurate even with small snap length)
- **Process mapping**: macOS `libproc` API enumerates all process TCP sockets every 3 seconds

## Tests

```bash
make test
```

Covers: RTT calculation, Karn's algorithm, retransmission detection, sequence wrapping, traffic filtering, aggregation math, percentile computation, quality scoring, use-case thresholds, database operations, downsampling, and libproc integration.

## License

MIT
