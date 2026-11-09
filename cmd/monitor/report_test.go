package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// seedReportHistories writes one bucket in the current month and one in the
// previous month, finalizing the previous one, so the report has both periods.
func seedReportHistories(t *testing.T, stateDir string) (current, previous string) {
	t.Helper()
	now := time.Now().UTC()
	current = persistence.FormatMonth(now)
	var err error
	previous, err = persistence.PreviousMonth(current)
	if err != nil {
		t.Fatal(err)
	}

	store := persistence.New(stateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		month string
		bytes int64
	}{
		{current, 1 << 30},
		{previous, 2 << 30},
	} {
		start, err := persistence.MonthStart(entry.month)
		if err != nil {
			t.Fatal(err)
		}
		history := persistence.NewHistory(entry.month)
		history.Set(persistence.Bucket{
			ResourceID: "ocid1.vnic.oc1.iad.example",
			Direction:  persistence.DirectionOutbound,
			Hour:       start,
			Bytes:      entry.bytes,
			Status:     persistence.StatusComplete,
			CapturedAt: now,
		})
		if entry.month == previous {
			history.Finalize(now)
		}
		if err := store.SaveHistory(history); err != nil {
			t.Fatal(err)
		}
	}
	return current, previous
}

func TestReportCommandDryRunPrintsSummary(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	// telegram.enabled=false: the dry run must still render the message.
	configPath := writeRunConfig(t, stateDir, false, false)
	current, previous := seedReportHistories(t, stateDir)
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--dry-run"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() != 0 {
		t.Errorf("dry run sent %d messages", sender.count())
	}
	for _, want := range []string{"resumen diario", "mes en curso " + current, "mes anterior " + previous, "es tráfico registrado, no el tráfico facturable"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err == nil {
		t.Error("report wrote state.json")
	}
}

func TestReportCommandSendsDigest(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, true)
	envPath := writeTelegramEnv(t)
	current, previous := seedReportHistories(t, stateDir)
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want 1", sender.count())
	}
	text := sender.all()[0]
	for _, want := range []string{"resumen diario", "mes en curso " + current, "mes anterior " + previous, "es tráfico registrado, no el tráfico facturable"} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(out, "resumen diario enviado") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err == nil {
		t.Error("report wrote state.json")
	}
}

func TestReportCommandNoDataSkipsSend(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, true)
	envPath := writeTelegramEnv(t)
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() != 0 {
		t.Errorf("sent = %d without data, want 0", sender.count())
	}
	if !strings.Contains(out, "no hay datos") {
		t.Errorf("output = %q", out)
	}
}

func TestReportCommandTelegramDisabledSkipsSend(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, false)
	seedReportHistories(t, stateDir)
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if sender.count() != 0 {
		t.Errorf("sent = %d with telegram disabled, want 0", sender.count())
	}
	if !strings.Contains(out, "telegram.enabled=false") {
		t.Errorf("output = %q", out)
	}
}

func TestReportCommandMissingCredentials(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, true)
	seedReportHistories(t, stateDir)
	stubFactories(t, nil, &stubSender{})

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--telegram-env", filepath.Join(t.TempDir(), "missing.env")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %s", code, errOut)
	}
}

func TestReportCommandSendFailureExits5(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, true)
	envPath := writeTelegramEnv(t)
	seedReportHistories(t, stateDir)
	sender := &stubSender{err: errors.New("stub: telegram no disponible")}
	stubFactories(t, nil, sender)

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 5 {
		t.Fatalf("exit = %d, want 5; stderr = %s", code, errOut)
	}
	if !strings.Contains(errOut, "no se pudo enviar el resumen diario") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestReportCommandCorruptHistoryExits4KeepsSending(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, true)
	envPath := writeTelegramEnv(t)
	_, previous := seedReportHistories(t, stateDir)
	store := persistence.New(stateDir)
	path, err := store.HistoryPath(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	sender := &stubSender{}
	stubFactories(t, nil, sender)

	var code int
	_, errOut := captureOutput(t, func() {
		code = execute([]string{"report", "--config", configPath, "--telegram-env", envPath})
	})
	if code != 4 {
		t.Fatalf("exit = %d, want 4; stderr = %s", code, errOut)
	}
	// The current month is still readable: the digest goes out with a warning
	// and the unit ends failed so the corruption is not hidden.
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want 1", sender.count())
	}
	if !strings.Contains(sender.all()[0], "AVISO: no se pudo leer el historial de "+previous) {
		t.Errorf("message missing warning:\n%s", sender.all()[0])
	}
	if !strings.Contains(errOut, "aviso de estado") {
		t.Errorf("stderr = %q", errOut)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("report moved the corrupt history: %v", statErr)
	}
	matches, err := filepath.Glob(filepath.Join(stateDir, "history", "*.corrupt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("quarantine files = %v, want none", matches)
	}
}

func TestReportCommandMissingConfig(t *testing.T) {
	var code int
	_, _ = captureOutput(t, func() {
		code = execute([]string{"report", "--config", filepath.Join(t.TempDir(), "missing.json")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
