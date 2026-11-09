package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// serverZone is a fixed zone used to pin the local hour of the reference
// instant without depending on the host where the tests run.
var serverZone = time.FixedZone("server", -5*3600)

func TestDailyReportDue(t *testing.T) {
	utc := time.Date(2026, 11, 7, 23, 5, 0, 0, time.UTC) // 18:05 in serverZone
	cases := []struct {
		name  string
		start time.Time
		hour  int
		want  bool
	}{
		{name: "in hour", start: utc.In(serverZone), hour: 18, want: true},
		{name: "one hour later", start: utc.In(serverZone).Add(time.Hour), hour: 18, want: false},
		{name: "zone of the instant matters", start: utc, hour: 18, want: false},
		{name: "disabled", start: utc.In(serverZone), hour: -1, want: false},
		{name: "midnight", start: time.Date(2026, 11, 8, 0, 1, 0, 0, serverZone), hour: 0, want: true},
	}
	for _, tc := range cases {
		if got := dailyReportDue(tc.start, tc.hour); got != tc.want {
			t.Errorf("%s: dailyReportDue(%s, %d) = %t, want %t",
				tc.name, tc.start, tc.hour, got, tc.want)
		}
	}
}

func TestRunSendsDailyReportAtConfiguredHour(t *testing.T) {
	cfg := runConfig(t)
	cfg.Telegram.DailyReportHour = 18
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(1 << 20)}
	sender := &fakeSender{}
	now := time.Date(2026, 11, 7, 18, 5, 0, 0, serverZone)

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: oci=%v telegram=%v", report.ExitCode, report.OCIError, report.TelegramError)
	}
	if !report.DailyReportDue || !report.DailyReportSent {
		t.Fatalf("daily report due/sent = %t/%t, want true/true", report.DailyReportDue, report.DailyReportSent)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want only the daily report", sender.count())
	}
	text := sender.all()[0]
	for _, want := range []string{
		"OCI Infrastructure Monitor — resumen diario",
		"mes en curso 2026-11",
		"mes anterior 2026-10",
		"es tráfico registrado, no el tráfico facturable",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
}

func TestRunDoesNotSendDailyReportOutsideHour(t *testing.T) {
	cfg := runConfig(t)
	cfg.Telegram.DailyReportHour = 18
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(1 << 20)}
	sender := &fakeSender{}
	now := time.Date(2026, 11, 7, 12, 5, 0, 0, serverZone)

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d", report.ExitCode)
	}
	if report.DailyReportDue || report.DailyReportSent {
		t.Errorf("daily report due/sent = %t/%t, want false/false", report.DailyReportDue, report.DailyReportSent)
	}
	if sender.count() != 0 {
		t.Errorf("sent = %d outside the configured hour, want 0", sender.count())
	}
}

func TestRunDailyReportDryRunDoesNotSend(t *testing.T) {
	cfg := runConfig(t)
	cfg.Telegram.DailyReportHour = 18
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(1 << 20)}
	sender := &fakeSender{}
	logger := &recordingLogger{}
	now := time.Date(2026, 11, 7, 18, 5, 0, 0, serverZone)

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now, DryRun: true, Logger: logger})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d", report.ExitCode)
	}
	if !report.DailyReportDue || report.DailyReportSent {
		t.Errorf("daily report due/sent = %t/%t, want true/false", report.DailyReportDue, report.DailyReportSent)
	}
	if sender.count() != 0 {
		t.Errorf("dry run sent %d messages", sender.count())
	}
	if !strings.Contains(logger.String(), "simulación: se habría enviado el resumen diario de las 18:00") {
		t.Errorf("log does not mention the simulated daily report:\n%s", logger.String())
	}
}

func TestRunDailyReportTelegramDisabledSkipsSend(t *testing.T) {
	cfg := runConfig(t)
	cfg.Telegram.Enabled = false
	cfg.Telegram.DailyReportHour = 18
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(1 << 20)}
	logger := &recordingLogger{}
	now := time.Date(2026, 11, 7, 18, 5, 0, 0, serverZone)

	report := Run(context.Background(), cfg, client, store, nil, RunOptions{Now: now, Logger: logger})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: telegram=%v", report.ExitCode, report.TelegramError)
	}
	if !report.DailyReportDue || report.DailyReportSent {
		t.Errorf("daily report due/sent = %t/%t, want true/false", report.DailyReportDue, report.DailyReportSent)
	}
	if !strings.Contains(logger.String(), "telegram.enabled=false") {
		t.Errorf("log does not explain the skip:\n%s", logger.String())
	}
}

func TestRunDailyReportFailureExitsTelegram(t *testing.T) {
	cfg := runConfig(t)
	cfg.Telegram.DailyReportHour = 18
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(1 << 20)}
	sender := &fakeSender{sendErr: errors.New("stub: telegram no disponible")}
	now := time.Date(2026, 11, 7, 18, 5, 0, 0, serverZone)

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitTelegram {
		t.Fatalf("exit = %d, want %d; errors: telegram=%v", report.ExitCode, ExitTelegram, report.TelegramError)
	}
	if report.DailyReportError == nil {
		t.Error("DailyReportError = nil")
	}
	if report.DailyReportSent {
		t.Error("DailyReportSent = true after a delivery failure")
	}
}

func TestSendDailyReportWithoutData(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	sender := &fakeSender{}

	sent, err := sendDailyReport(context.Background(), cfg, store, sender, time.Date(2026, 11, 7, 23, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("sendDailyReport() error = %v", err)
	}
	if sent {
		t.Error("sent = true without data")
	}
	if sender.count() != 0 {
		t.Errorf("sent = %d, want 0", sender.count())
	}
}
