package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPath is the default location of the JSON configuration file.
const DefaultPath = "/etc/oci-infrastructure-monitor/config.json"

// Config is the root configuration.
type Config struct {
	OCI      OCI      `json:"oci"`
	Network  Network  `json:"network"`
	Telegram Telegram `json:"telegram"`
	Storage  Storage  `json:"storage"`
	Logging  Logging  `json:"logging"`
}

// OCI holds the OCI connection settings.
type OCI struct {
	Region string `json:"region"`
	Auth   string `json:"auth"`
}

// Network holds the network monitoring settings.
type Network struct {
	Enabled                        bool     `json:"enabled"`
	CompartmentOCID                string   `json:"compartment_ocid"`
	InstanceOCID                   string   `json:"instance_ocid"`
	VNICOCIDs                      []string `json:"vnic_ocids"`
	MonthlyLimitGiB                float64  `json:"monthly_limit_gib"`
	AlertPercentages               []int    `json:"alert_percentages"`
	HighTraffic6HGiB               float64  `json:"high_traffic_6h_gib"`
	HighTrafficCooldownHours       int      `json:"high_traffic_cooldown_hours"`
	RequeryWindowHours             int      `json:"requery_window_hours"`
	PublicationLatencyHours        int      `json:"publication_latency_hours"`
	NoDataStreakHours              int      `json:"no_data_streak_hours"`
	CollectionFailureThreshold     int      `json:"collection_failure_threshold"`
	CollectionFailureCooldownHours int      `json:"collection_failure_cooldown_hours"`
	CollectInbound                 bool     `json:"collect_inbound"`
}

// Telegram holds the Telegram notification settings. Credentials live in telegram.env.
type Telegram struct {
	Enabled bool `json:"enabled"`
	// DailyReportHour is the local hour (0-23) of the run that also sends the
	// daily report; -1 disables it. The default (18) is applied by Default, so
	// an existing configuration file without the key keeps the previous
	// behavior. The hour is compared against the server's local time, the same
	// zone the systemd timer uses.
	DailyReportHour int `json:"daily_report_hour"`
}

// Storage holds the persistent state settings.
type Storage struct {
	StateDir string `json:"state_dir"`
}

// Logging holds the logging settings.
type Logging struct {
	Level string `json:"level"`
}

// Default returns a configuration with the documented defaults applied.
func Default() Config {
	return Config{
		OCI: OCI{
			Auth: "instance_principal",
		},
		Network: Network{
			Enabled:                        true,
			AlertPercentages:               []int{50, 75, 90, 100},
			HighTrafficCooldownHours:       24,
			RequeryWindowHours:             48,
			PublicationLatencyHours:        2,
			NoDataStreakHours:              6,
			CollectionFailureThreshold:     3,
			CollectionFailureCooldownHours: 24,
		},
		Telegram: Telegram{Enabled: true, DailyReportHour: 18},
		Storage:  Storage{StateDir: "/var/lib/oci-infrastructure-monitor"},
		Logging:  Logging{Level: "info"},
	}
}

// Load reads and validates a configuration file.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration %q: %w", path, err)
	}
	defer f.Close()

	cfg, err := Parse(f)
	if err != nil {
		return Config{}, fmt.Errorf("configuration %q: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates a configuration from r.
func Parse(r io.Reader) (Config, error) {
	cfg := Default()

	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	// Reject a second JSON value or trailing garbage after the configuration.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("configuration contains more than one JSON value")
		}
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks all configuration invariants.
func (c Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.OCI.Region) == "" {
		add("oci.region is required")
	}
	if c.OCI.Auth != "instance_principal" {
		add("oci.auth must be %q", "instance_principal")
	}

	if len(c.Network.AlertPercentages) != 4 {
		add("network.alert_percentages must contain exactly 4 values")
	} else {
		previous := 0
		for i, p := range c.Network.AlertPercentages {
			if p <= 0 || p > 100 {
				add("network.alert_percentages[%d] must be between 1 and 100", i)
			}
			if i > 0 && p <= previous {
				add("network.alert_percentages must be strictly increasing")
			}
			previous = p
		}
		if c.Network.AlertPercentages[3] != 100 {
			add("network.alert_percentages[3] must be 100")
		}
	}

	if c.Network.RequeryWindowHours < 1 {
		add("network.requery_window_hours must be at least 1")
	}
	if c.Network.HighTrafficCooldownHours < 1 {
		add("network.high_traffic_cooldown_hours must be at least 1")
	}
	if c.Network.PublicationLatencyHours < 1 {
		add("network.publication_latency_hours must be at least 1")
	}
	if c.Network.NoDataStreakHours < 1 {
		add("network.no_data_streak_hours must be at least 1")
	}
	if c.Network.CollectionFailureThreshold < 1 {
		add("network.collection_failure_threshold must be at least 1")
	}
	if c.Network.CollectionFailureCooldownHours < 1 {
		add("network.collection_failure_cooldown_hours must be at least 1")
	}

	if c.Network.MonthlyLimitGiB < 0 || math.IsNaN(c.Network.MonthlyLimitGiB) || math.IsInf(c.Network.MonthlyLimitGiB, 0) {
		add("network.monthly_limit_gib must be a finite number greater than or equal to 0")
	}
	if c.Network.HighTraffic6HGiB < 0 || math.IsNaN(c.Network.HighTraffic6HGiB) || math.IsInf(c.Network.HighTraffic6HGiB, 0) {
		add("network.high_traffic_6h_gib must be a finite number greater than or equal to 0")
	}

	if c.Telegram.DailyReportHour < -1 || c.Telegram.DailyReportHour > 23 {
		add("telegram.daily_report_hour must be -1 (disabled) or an hour between 0 and 23")
	}

	if c.Network.Enabled {
		if !isOCID(c.Network.CompartmentOCID) {
			add("network.compartment_ocid must be a valid OCID")
		}
		if !isOCID(c.Network.InstanceOCID) {
			add("network.instance_ocid must be a valid OCID")
		}
		if len(c.Network.VNICOCIDs) == 0 {
			add("network.vnic_ocids must contain at least one OCID when network is enabled")
		}
		seen := make(map[string]bool, len(c.Network.VNICOCIDs))
		for i, id := range c.Network.VNICOCIDs {
			if !isOCID(id) {
				add("network.vnic_ocids[%d] must be a valid OCID", i)
			}
			if seen[id] {
				add("network.vnic_ocids contains duplicate %q", id)
			}
			seen[id] = true
		}
		if c.Network.MonthlyLimitGiB <= 0 {
			add("network.monthly_limit_gib must be greater than 0 when network is enabled")
		}
		if c.Network.HighTraffic6HGiB <= 0 {
			add("network.high_traffic_6h_gib must be greater than 0 when network is enabled")
		}
	}

	if !filepath.IsAbs(c.Storage.StateDir) {
		add("storage.state_dir must be an absolute path")
	}

	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		add("logging.level must be one of debug, info, warn, error")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func isOCID(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 5 {
		return false
	}
	if parts[0] != "ocid1" || parts[1] == "" || parts[4] == "" {
		return false
	}
	for _, part := range parts[1:] {
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
