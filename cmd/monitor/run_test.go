package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/telegram"
)

type stubOCIClient struct {
	value float64
	err   error
	calls int
}

func (s *stubOCIClient) Summarize(_ context.Context, query oci.Query) ([]oci.Series, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	vnic := queryVNIC(query.MQL)
	var points []oci.Point
	for hour := query.StartTime.UTC().Truncate(time.Hour); hour.Before(query.EndTime); hour = hour.Add(time.Hour) {
		points = append(points, oci.Point{Time: hour.Add(time.Hour), Value: s.value})
	}
	return []oci.Series{{ResourceID: vnic, Points: points}}, nil
}

func (s *stubOCIClient) ListMetrics(context.Context, string, string, map[string]string) ([]oci.MetricDefinition, error) {
	return nil, nil
}

func queryVNIC(mql string) string {
	const prefix = `resourceId = "`
	index := strings.Index(mql, prefix)
	if index < 0 {
		return ""
	}
	rest := mql[index+len(prefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

type stubSender struct {
	mu    sync.Mutex
	texts []string
	err   error
}

func (s *stubSender) SendMessage(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	return s.err
}

func (s *stubSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.texts)
}

func (s *stubSender) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

func stubFactories(t *testing.T, client oci.Client, sender telegram.Sender) {
	t.Helper()
	oldClient, oldSender := newOCIClient, newTelegramSender
	t.Cleanup(func() {
		newOCIClient = oldClient
		newTelegramSender = oldSender
	})
	if client != nil {
		newOCIClient = func(string) (oci.Client, error) { return client, nil }
	}
	if sender != nil {
		newTelegramSender = func(telegram.Credentials) (telegram.Sender, error) { return sender, nil }
	}
}

func testOCID(kind, fill string) string {
	return "ocid1." + kind + ".oc1.iad." + strings.Repeat(fill, 64)
}

func writeRunConfig(t *testing.T, stateDir string, networkEnabled, telegramEnabled bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	var content string
	if networkEnabled {
		content = fmt.Sprintf(`{
  "oci": {"region": "us-ashburn-1"},
  "network": {
    "enabled": true,
    "compartment_ocid": %q,
    "instance_ocid": %q,
    "vnic_ocids": [%q],
    "monthly_limit_gib": 1,
    "alert_percentages": [50, 75, 90, 100],
    "high_traffic_6h_gib": 1000,
    "requery_window_hours": 48,
    "publication_latency_hours": 2,
    "no_data_streak_hours": 6,
    "collection_failure_threshold": 3,
    "collection_failure_cooldown_hours": 24,
    "collect_inbound": false
  },
  "telegram": {"enabled": %t},
  "storage": {"state_dir": %q},
  "logging": {"level": "info"}
}`, testOCID("compartment", "c"), testOCID("instance", "a"), testOCID("vnic", "b"), telegramEnabled, stateDir)
	} else {
		content = fmt.Sprintf(`{
  "oci": {"region": "us-ashburn-1"},
  "network": {"enabled": false},
  "telegram": {"enabled": %t},
  "storage": {"state_dir": %q},
  "logging": {"level": "info"}
}`, telegramEnabled, stateDir)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTelegramEnv(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telegram.env")
	content := "TELEGRAM_BOT_TOKEN=123:test-token\nTELEGRAM_CHAT_ID=42\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUsageMentionsRunAndTestTelegram(t *testing.T) {
	var buf strings.Builder
	usage(&buf)
	for _, want := range []string{"run", "test-telegram", "status", "check"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, buf.String())
		}
	}
}

func TestRunCommandNetworkDisabledExitsZero(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, false)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--config", configPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "consultas_oci=0") {
		t.Errorf("output = %q", out)
	}
}

func TestRunCommandDryRunWithStubsWritesNothing(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, true, true)
	client := &stubOCIClient{value: 10 * 1024 * 1024}
	stubFactories(t, client, nil)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--config", configPath, "--dry-run"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "simulación") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err == nil {
		t.Error("dry run wrote state.json")
	}
}

func TestRunCommandSendsAlertWithStubs(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, true, true)
	envPath := writeTelegramEnv(t)
	client := &stubOCIClient{value: 10 * 1024 * 1024}
	sender := &stubSender{}
	stubFactories(t, client, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() == 0 {
		t.Fatalf("no message sent; output = %s", out)
	}
	if !strings.Contains(out, "enviadas=") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err != nil {
		t.Errorf("state.json was not written: %v", err)
	}
}

func TestRunCommandMissingConfig(t *testing.T) {
	var code int
	_, _ = captureOutput(t, func() {
		code = execute([]string{"run", "--config", filepath.Join(t.TempDir(), "missing.json")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRunCommandMissingCredentials(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, true, true)
	stubFactories(t, &stubOCIClient{value: 1}, &stubSender{})

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--config", configPath, "--telegram-env", filepath.Join(t.TempDir(), "missing.env")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errOut)
	}
}

func TestRunCommandLockBusyExits6(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, true, true)
	envPath := writeTelegramEnv(t)
	store := persistence.New(stateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	stubFactories(t, &stubOCIClient{value: 1}, &stubSender{})

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 6 {
		t.Fatalf("exit = %d, want 6; stdout = %s stderr = %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "concurrencia") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestRunCommandRejectsTokenFlag(t *testing.T) {
	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"run", "--token", "super-secret"})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if strings.Contains(out, "super-secret") || strings.Contains(errOut, "super-secret") {
		t.Errorf("the token value was echoed: stdout=%q stderr=%q", out, errOut)
	}
}

func TestTestTelegramCommandSendsExplicitMessage(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	// telegram.enabled=false: test-telegram must still work with credentials.
	configPath := writeRunConfig(t, stateDir, false, false)
	envPath := writeTelegramEnv(t)
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"test-telegram", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want 1", sender.count())
	}
	text := sender.all()[0]
	for _, want := range []string{"mensaje de prueba", "tráfico registrado, no el tráfico facturable"} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(out, "mensaje de prueba enviado") {
		t.Errorf("output = %q", out)
	}
}

func TestTestTelegramCommandMissingCredentials(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, false)

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"test-telegram", "--config", configPath, "--telegram-env", filepath.Join(t.TempDir(), "missing.env")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errOut)
	}
}

func TestTestTelegramCommandSendFailureExits5(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, false)
	envPath := writeTelegramEnv(t)
	sender := &stubSender{err: errors.New("telegram down")}
	stubFactories(t, nil, sender)

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"test-telegram", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 5 {
		t.Fatalf("exit = %d, want 5; stderr = %s", code, errOut)
	}
}
