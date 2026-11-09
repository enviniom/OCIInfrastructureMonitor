package config

import (
	"strings"
	"testing"
)

func testOCID(kind, unique string) string {
	return "ocid1." + kind + ".oc1.iad." + unique
}

func validConfig() Config {
	cfg := Default()
	cfg.OCI.Region = "us-ashburn-1"
	cfg.Network.CompartmentOCID = testOCID("compartment", strings.Repeat("c", 64))
	cfg.Network.InstanceOCID = testOCID("instance", strings.Repeat("a", 64))
	cfg.Network.VNICOCIDs = []string{testOCID("vnic", strings.Repeat("b", 64))}
	cfg.Network.MonthlyLimitGiB = 10240
	cfg.Network.HighTraffic6HGiB = 100
	return cfg
}

func TestValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(strings.NewReader(`{
		"oci": {"region": "us-ashburn-1"},
		"network": {"enabled": false}
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.OCI.Auth != "instance_principal" {
		t.Errorf("auth default = %q, want instance_principal", cfg.OCI.Auth)
	}
	if cfg.Network.RequeryWindowHours != 48 {
		t.Errorf("requery default = %d, want 48", cfg.Network.RequeryWindowHours)
	}
	if cfg.Network.PublicationLatencyHours != 2 {
		t.Errorf("publication latency default = %d, want 2", cfg.Network.PublicationLatencyHours)
	}
	if cfg.Network.HighTrafficCooldownHours != 24 {
		t.Errorf("high traffic cooldown default = %d, want 24", cfg.Network.HighTrafficCooldownHours)
	}
	if got := cfg.Network.AlertPercentages; len(got) != 4 || got[3] != 100 {
		t.Errorf("alert percentages default = %v, want [50 75 90 100]", got)
	}
	if cfg.Storage.StateDir != "/var/lib/oci-infrastructure-monitor" {
		t.Errorf("state dir default = %q", cfg.Storage.StateDir)
	}
	if cfg.Telegram.DailyReportHour != 18 {
		t.Errorf("daily report hour default = %d, want 18", cfg.Telegram.DailyReportHour)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("logging level default = %q, want info", cfg.Logging.Level)
	}
}

func TestDailyReportHourValidation(t *testing.T) {
	for _, hour := range []int{-1, 0, 18, 23} {
		cfg := validConfig()
		cfg.Telegram.DailyReportHour = hour
		if err := cfg.Validate(); err != nil {
			t.Errorf("hour %d rejected: %v", hour, err)
		}
	}
	for _, hour := range []int{-2, 24} {
		cfg := validConfig()
		cfg.Telegram.DailyReportHour = hour
		if err := cfg.Validate(); err == nil {
			t.Errorf("hour %d accepted, want a validation error", hour)
		}
	}
}

func TestParseExampleFile(t *testing.T) {
	cfg, err := Load("../../configs/config.example.json")
	if err != nil {
		t.Fatalf("Load(example) error = %v", err)
	}
	if !cfg.Network.Enabled {
		t.Error("example network.enabled = false, want true")
	}
	if len(cfg.Network.VNICOCIDs) != 1 {
		t.Errorf("example vnic_ocids length = %d, want 1", len(cfg.Network.VNICOCIDs))
	}
}

func TestUnknownField(t *testing.T) {
	_, err := Parse(strings.NewReader(`{
		"oci": {"region": "us-ashburn-1"},
		"network": {"enabled": false},
		"unknown": true
	}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Parse() error = %v, want unknown field error", err)
	}
}

func TestInvalidJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader(`{`)); err == nil {
		t.Fatal("Parse() accepted invalid JSON")
	}
}

func TestTrailingJSON(t *testing.T) {
	_, err := Parse(strings.NewReader(`{
		"oci": {"region": "us-ashburn-1"},
		"network": {"enabled": false}
	} {}`))
	if err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("Parse() error = %v, want trailing JSON error", err)
	}
}

func TestInvalidAlertPercentages(t *testing.T) {
	tests := []struct {
		name   string
		values []int
	}{
		{"too few", []int{50, 75, 100}},
		{"too many", []int{50, 60, 75, 90, 100}},
		{"not increasing", []int{50, 50, 90, 100}},
		{"first zero", []int{0, 50, 75, 100}},
		{"last not 100", []int{50, 75, 90, 99}},
		{"outside range", []int{50, 75, 90, 101}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Network.AlertPercentages = tc.values
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %v", tc.values)
			}
		})
	}
}

func TestInvalidHighTrafficCooldown(t *testing.T) {
	cfg := validConfig()
	cfg.Network.HighTrafficCooldownHours = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "high_traffic_cooldown_hours") {
		t.Fatalf("Validate() error = %v, want high_traffic_cooldown_hours", err)
	}
}

func TestNetworkEnabledRequiresVNIC(t *testing.T) {
	cfg := validConfig()
	cfg.Network.VNICOCIDs = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted enabled network without VNICs")
	}
}

func TestMalformedOCID(t *testing.T) {
	cfg := validConfig()
	cfg.Network.InstanceOCID = "not-an-ocid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted malformed instance OCID")
	}
}

func TestDuplicateVNIC(t *testing.T) {
	cfg := validConfig()
	cfg.Network.VNICOCIDs = append(cfg.Network.VNICOCIDs, cfg.Network.VNICOCIDs[0])
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate VNIC OCIDs")
	}
}

func TestRelativeStateDir(t *testing.T) {
	cfg := validConfig()
	cfg.Storage.StateDir = "var/lib/oci-infrastructure-monitor"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a relative state_dir")
	}
}

func TestInvalidLogLevel(t *testing.T) {
	cfg := validConfig()
	cfg.Logging.Level = "trace"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an invalid logging level")
	}
}

func TestNetworkDisabledAllowsZeroLimit(t *testing.T) {
	cfg := validConfig()
	cfg.Network.Enabled = false
	cfg.Network.CompartmentOCID = ""
	cfg.Network.InstanceOCID = ""
	cfg.Network.VNICOCIDs = nil
	cfg.Network.MonthlyLimitGiB = 0
	cfg.Network.HighTraffic6HGiB = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected disabled network with zero limits: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/does/not/exist/config.json"); err == nil {
		t.Fatal("Load() accepted a missing file")
	}
}

func TestOCIDValidation(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"instance", "ocid1.instance.oc1.iad." + strings.Repeat("a", 64), true},
		{"global", "ocid1.tenancy.oc1.." + strings.Repeat("a", 64), true},
		{"wrong prefix", "ocid2.instance.oc1.iad." + strings.Repeat("a", 64), false},
		{"missing part", "ocid1.instance.oc1.iad", false},
		{"empty unique", "ocid1.instance.oc1.iad.", false},
		{"uppercase", "ocid1.instance.oc1.iad." + strings.Repeat("A", 64), false},
		{"spaces", "ocid1.instance.oc1.iad.aaaa aaaa", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOCID(tc.value); got != tc.want {
				t.Errorf("isOCID(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
