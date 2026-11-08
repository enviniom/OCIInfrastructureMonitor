package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/app"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func TestFormatThousands(t *testing.T) {
	cases := map[int64]string{
		0:          "0",
		999:        "999",
		1000:       "1.000",
		1234567890: "1.234.567.890",
		-1234567:   "-1.234.567",
	}
	for value, want := range cases {
		if got := formatThousands(value); got != want {
			t.Errorf("formatThousands(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	if got := formatBytes(1 << 30); got != "1,00 GiB (1.073.741.824 bytes)" {
		t.Errorf("formatBytes(1 GiB) = %q", got)
	}
	if got := formatSigned(-1234); got != "-1.234" {
		t.Errorf("formatSigned(-1234) = %q", got)
	}
	if got := formatSigned(1234); got != "+1.234" {
		t.Errorf("formatSigned(1234) = %q", got)
	}
}

func statusReport() app.StatusReport {
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	lastRun := time.Date(2026, 11, 7, 13, 30, 0, 0, time.UTC)
	lastVisible := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	return app.StatusReport{
		StateDir:    "/var/lib/oci-infrastructure-monitor",
		StateExists: true,
		State: persistence.State{
			SchemaVersion:       persistence.CurrentSchemaVersion,
			LastRun:             &lastRun,
			LastSuccessfulQuery: &lastRun,
			Pending:             []persistence.Notification{{Kind: persistence.NotificationMonthlyThreshold, CreatedAt: lastRun}},
		},
		Months: []app.MonthStatus{
			{
				Month:      "2026-11",
				Label:      "actual",
				HasHistory: true,
				Summary: accumulate.Summary{
					Month:    "2026-11",
					HasData:  true,
					Status:   persistence.StatusComplete,
					Outbound: accumulate.DirectionSummary{Direction: persistence.DirectionOutbound, Bytes: 1234567890, Buckets: 168, Complete: 168, Status: persistence.StatusComplete},
				},
			},
			{
				Month:       "2026-10",
				Label:       "anterior",
				HasHistory:  true,
				Grace:       true,
				Corrections: persistence.MonthCorrections{BucketsCorrected: 2, DeltaBytes: -1234, UpdatedAt: at},
				Summary: accumulate.Summary{
					Month:    "2026-10",
					HasData:  true,
					Status:   persistence.StatusIncomplete,
					Outbound: accumulate.DirectionSummary{Direction: persistence.DirectionOutbound, Bytes: 500, Buckets: 744, Complete: 700, NoData: 10, Incomplete: 34, Status: persistence.StatusIncomplete, Reason: "34 buckets incompletos"},
				},
			},
		},
		Warnings: []app.VNICWarning{{OCID: "ocid1.vnic.oc1.iad.a", Since: lastRun, LastVisible: &lastVisible}},
	}
}

func TestWriteStatus(t *testing.T) {
	var buf bytes.Buffer
	writeStatus(&buf, statusReport())
	out := buf.String()

	wants := []string{
		"oci-infrastructure-monitor — estado",
		"directorio: /var/lib/oci-infrastructure-monitor",
		fmt.Sprintf("esquema: %d", persistence.CurrentSchemaVersion),
		"última ejecución: 2026-11-07 13:30:00 UTC",
		"notificaciones pendientes: 1",
		"mes actual: 2026-11 (abierto)",
		"saliente: 1,15 GiB (1.234.567.890 bytes)",
		"acumulado: completo",
		"buckets: 168 (complete 168, provisional 0, no_data 0, incomplete 0)",
		"mes anterior: 2026-10 (no finalizado (en gracia))",
		"acumulado: incompleto (34 buckets incompletos)",
		"correcciones durante la gracia: 2 buckets, -1.234 bytes",
		"avisos:",
		"dejó de aparecer en métricas; último dato 2026-11-05T12:00:00Z",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteStatusFinalizedAndNoHistory(t *testing.T) {
	finalizedAt := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	report := app.StatusReport{
		StateDir: "/state",
		Months: []app.MonthStatus{
			{Month: "2026-10", Label: "anterior", HasHistory: true, Finalized: true, FinalizedAt: &finalizedAt},
			{Month: "2026-09", Label: "anterior", HasHistory: false},
		},
	}
	var buf bytes.Buffer
	writeStatus(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "mes anterior: 2026-10 (finalizado el 2026-11-03 00:00:00 UTC)") {
		t.Errorf("missing finalized label:\n%s", out)
	}
	if !strings.Contains(out, "  sin historial") {
		t.Errorf("missing no-history line:\n%s", out)
	}
	if !strings.Contains(out, "estado: sin estado registrado") {
		t.Errorf("missing no-state line:\n%s", out)
	}
}

func TestWriteStatusWithErrors(t *testing.T) {
	report := statusReport()
	report.StateErr = persistence.ErrCorrupt
	report.Months[1].Err = persistence.ErrCorrupt
	report.Errors = []error{persistence.ErrCorrupt}

	var buf bytes.Buffer
	writeStatus(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "esquema: ILEGIBLE") {
		t.Errorf("missing unreadable schema:\n%s", out)
	}
	if !strings.Contains(out, "errores:") {
		t.Errorf("missing errors section:\n%s", out)
	}
}

func writeStatusConfig(t *testing.T, stateDir string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
  "oci": {"region": "us-ashburn-1"},
  "network": {"enabled": false},
  "storage": {"state_dir": "` + stateDir + `"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()

	fn()

	outW.Close()
	errW.Close()
	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes)
}

func TestStatusCommandExitCodes(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeStatusConfig(t, stateDir)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"status", "--config", configPath})
	})
	if code != 0 {
		t.Fatalf("status exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "sin estado registrado") {
		t.Errorf("output = %q", out)
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut = captureOutput(t, func() {
		code = execute([]string{"status", "--config", configPath})
	})
	if code != 4 {
		t.Fatalf("corrupt status exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(errOut, "error de persistencia") {
		t.Errorf("stderr = %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.json")); err != nil {
		t.Errorf("status moved the corrupt state: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(stateDir, "state.json.corrupt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("quarantine files = %v, want none", matches)
	}
}

func TestStatusCommandMissingConfig(t *testing.T) {
	var code int
	_, _ = captureOutput(t, func() {
		code = execute([]string{"status", "--config", filepath.Join(t.TempDir(), "missing.json")})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestUsageMentionsStatus(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	if !strings.Contains(buf.String(), "status") {
		t.Errorf("usage does not mention status:\n%s", buf.String())
	}
}

func TestStatusCommandUnknownSchemaExitCode(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"schema_version": 4}`
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeStatusConfig(t, stateDir)

	var code int
	out, _ := captureOutput(t, func() {
		code = execute([]string{"status", "--config", configPath})
	})
	if code != 4 {
		t.Fatalf("exit = %d, want 4", code)
	}
	if !strings.Contains(out, "ILEGIBLE") {
		t.Errorf("output = %q", out)
	}
	got, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		t.Fatalf("state file moved: %v", err)
	}
	if string(got) != content {
		t.Errorf("state overwritten: %q", got)
	}
}
