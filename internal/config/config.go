package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Capture CaptureConfig `toml:"capture"`
	Filter  FilterConfig  `toml:"filter"`
	Storage StorageConfig `toml:"storage"`
	Web     WebConfig     `toml:"web"`
}

type CaptureConfig struct {
	Interface   string `toml:"interface"`
	SnapLen     int32  `toml:"snap_len"`
	Promiscuous bool   `toml:"promiscuous"`
}

type FilterConfig struct {
	ExcludeNetworks []string `toml:"exclude_networks"`
	LocalNetworks   []string `toml:"local_networks"`
}

type StorageConfig struct {
	DBPath       string `toml:"db_path"`
	Retention1s  string `toml:"retention_1s"`
	Retention1m  string `toml:"retention_1m"`
	Retention1h  string `toml:"retention_1h"`
}

type WebConfig struct {
	Listen string `toml:"listen"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Capture: CaptureConfig{
			Interface:   "en0",
			SnapLen:     96,
			Promiscuous: false,
		},
		Filter: FilterConfig{
			ExcludeNetworks: []string{
				"127.0.0.0/8",
				"169.254.0.0/16",
				"172.17.0.0/16",
				"172.18.0.0/16",
			},
			LocalNetworks: []string{
				"10.0.0.0/8",
				"192.168.0.0/16",
			},
		},
		Storage: StorageConfig{
			DBPath:      filepath.Join(home, ".network-tracker", "data.db"),
			Retention1s: "1h",
			Retention1m: "7d",
			Retention1h: "90d",
		},
		Web: WebConfig{
			Listen: "127.0.0.1:8787",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, err
	}
	cfg.Storage.DBPath = expandHome(cfg.Storage.DBPath)
	return cfg, nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
