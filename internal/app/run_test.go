package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/telegram"
)

type fakeSender struct {
	mu      sync.Mutex
	texts   []string
	sendErr error
	onSend  func(text string) error
}

func (f *fakeSender) SendMessage(_ context.Context, text string) error {
	f.mu.Lock()
	f.texts = append(f.texts, text)
	f.mu.Unlock()
	if f.onSend != nil {
		if err := f.onSend(text); err != nil {
			return err
		}
	}
	return f.sendErr
}

func (f *fakeSender) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.texts))
	copy(out, f.texts)
	return out
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

type recordingLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *recordingLogger) write(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}
func (l *recordingLogger) Debugf(format string, args ...any) { l.write(format, args...) }
func (l *recordingLogger) Infof(format string, args ...any)  { l.write(format, args...) }
func (l *recordingLogger) Warnf(format string, args ...any)  { l.write(format, args...) }
func (l *recordingLogger) Errorf(format string, args ...any) { l.write(format, args...) }
func (l *recordingLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func runConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := collectConfig(t)
	cfg.Network.MonthlyLimitGiB = 100
	cfg.Network.HighTraffic6HGiB = 1000
	cfg.Telegram.Enabled = true
	// The daily report is exercised by its own tests; keep the rest independent
	// from the hour of the reference instant.
	cfg.Telegram.DailyReportHour = -1
	return cfg
}

func seedNotification(t *testing.T, store *persistence.Store, notifications ...persistence.Notification) {
	t.Helper()
	state := persistence.NewState()
	state.Pending = append(state.Pending, notifications...)
	if err := store.SaveState(state); err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

func hasKind(sent []Delivered, kind string) bool {
	for _, delivered := range sent {
		if delivered.Kind == kind {
			return true
		}
	}
	return false
}

func countKind(sent []Delivered, kind string) int {
	count := 0
	for _, delivered := range sent {
		if delivered.Kind == kind {
			count++
		}
	}
	return count
}

func TestRunSendsMonthlyAlertAndMarksIt(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: config=%v persistence=%v oci=%v telegram=%v",
			report.ExitCode, report.ConfigError, report.PersistenceError, report.OCIError, report.TelegramError)
	}
	if !hasKind(report.Sent, persistence.NotificationMonthlyThreshold) {
		t.Fatalf("sent = %+v, want a monthly alert", report.Sent)
	}
	if sender.count() != len(report.Sent) {
		t.Errorf("sender count = %d, report sent = %d", sender.count(), len(report.Sent))
	}
	for _, text := range sender.all() {
		if !strings.Contains(text, "alerta mensual") {
			t.Errorf("unexpected message:\n%s", text)
		}
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if len(state.Pending) != 0 {
		t.Errorf("pending = %+v, want empty", state.Pending)
	}
	levels := state.Alerts.MonthlyAlerts["2026-11"]
	if len(levels) != 4 {
		t.Errorf("monthly alerts = %v, want every crossed level recorded", levels)
	}
	if state.LastRun == nil || !state.LastRun.Equal(now) {
		t.Errorf("last_run = %v, want %s", state.LastRun, now)
	}
}

func TestRunDryRunWritesNothingAndSendsNothing(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now, DryRun: true})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: %v %v %v", report.ExitCode, report.OCIError, report.PersistenceError, report.TelegramError)
	}
	if report.WouldSend == 0 {
		t.Error("WouldSend = 0, want at least one simulated alert")
	}
	if sender.count() != 0 {
		t.Errorf("dry run sent %d messages", sender.count())
	}
	if store.HasState() {
		t.Error("dry run wrote state.json")
	}
	if store.HasHistory("2026-11") {
		t.Error("dry run wrote a history file")
	}
}

func TestRunDryRunWithOCIFailureExits3AndWritesNothing(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	client := &fakeMonitoring{handler: func(oci.Query) ([]oci.Series, error) {
		return nil, errors.New("oci unavailable")
	}}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now, DryRun: true})
	if report.ExitCode != ExitOCI {
		t.Fatalf("exit = %d, want 3", report.ExitCode)
	}
	if sender.count() != 0 {
		t.Error("dry run sent a message")
	}
	if store.HasState() || store.HasHistory("2026-11") {
		t.Error("dry run wrote state or history")
	}
}

func TestRunLockBusyReturns6BeforeReadingState(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock()
	if err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	defer lock.Close()

	client := &fakeMonitoring{handler: fullSeriesHandler(1)}
	sender := &fakeSender{}
	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: utcHour(2026, 11, 7, 13)})
	if report.ExitCode != ExitConcurrent {
		t.Fatalf("exit = %d, want 6", report.ExitCode)
	}
	if client.callCount() != 0 {
		t.Errorf("OCI queries = %d, want 0", client.callCount())
	}
	if store.HasState() {
		t.Error("state was written despite the lock being busy")
	}
}

func TestRunOCIFailureDoesNotAbortAndSendsPending(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     50,
		CreatedAt: now.Add(-time.Hour),
		Text:      "alerta mensual pendiente de prueba",
	})
	client := &fakeMonitoring{handler: func(oci.Query) ([]oci.Series, error) {
		return nil, errors.New("oci down")
	}}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOCI {
		t.Fatalf("exit = %d, want 3 (OCI outranks Telegram)", report.ExitCode)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want the pending notification to go out", sender.count())
	}
	if !strings.Contains(sender.all()[0], "alerta mensual pendiente de prueba") {
		t.Errorf("unexpected text: %q", sender.all()[0])
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if len(state.Pending) != 0 {
		t.Errorf("pending = %+v, want empty", state.Pending)
	}
	if state.ConsecutiveFailures != 1 {
		t.Errorf("consecutive failures = %d, want 1", state.ConsecutiveFailures)
	}
	summary := report.Collect.Summaries["2026-11"]
	if summary.Outbound.Incomplete == 0 || summary.Status != persistence.StatusIncomplete {
		t.Errorf("summary = %+v, want an incomplete total", summary)
	}
}

func TestRunPartialVNICFailure(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.VNICOCIDs = []string{testOCID("vnic", "b"), testOCID("vnic", "d")}
	okVNIC := cfg.Network.VNICOCIDs[0]
	failingVNIC := cfg.Network.VNICOCIDs[1]
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)

	client := &fakeMonitoring{handler: func(query oci.Query) ([]oci.Series, error) {
		if parseVNIC(query.MQL) == failingVNIC {
			return nil, errors.New("vnic query failed")
		}
		return fullSeries(query, 1), nil
	}}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOCI {
		t.Fatalf("exit = %d, want 3", report.ExitCode)
	}
	if report.Collect.Succeeded != 1 || len(report.Collect.Failures) != 1 {
		t.Fatalf("report = %+v", report.Collect)
	}
	history, _, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	failedBuckets := 0
	for _, bucket := range history.Buckets {
		if bucket.ResourceID == failingVNIC && bucket.Status == persistence.StatusIncomplete {
			failedBuckets++
		}
	}
	if failedBuckets == 0 {
		t.Error("the failing VNIC has no incomplete buckets")
	}
	healthy := 0
	for _, bucket := range history.Buckets {
		if bucket.ResourceID == okVNIC && bucket.Status != persistence.StatusIncomplete {
			healthy++
		}
	}
	if healthy == 0 {
		t.Error("the healthy VNIC was not collected")
	}
	state, _ := store.LoadState()
	if state.ConsecutiveFailures != 1 {
		t.Errorf("consecutive failures = %d, want 1", state.ConsecutiveFailures)
	}
}

func TestRunNeverVisibleCountsAsCollectionFailureAndRecovers(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.CollectionFailureThreshold = 3
	cfg.Network.CollectionFailureCooldownHours = 24
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	sender := &fakeSender{}

	empty := &fakeMonitoring{handler: func(query oci.Query) ([]oci.Series, error) {
		return []oci.Series{{ResourceID: parseVNIC(query.MQL)}}, nil
	}}
	for i := 0; i < 3; i++ {
		report := Run(context.Background(), cfg, empty, store, sender, RunOptions{Now: now.Add(time.Duration(i) * time.Hour)})
		if report.ExitCode != ExitOK {
			t.Fatalf("run %d exit = %d, want 0 with a warning", i, report.ExitCode)
		}
		if len(report.Collect.NeverVisible) != 1 {
			t.Fatalf("run %d never visible = %+v", i, report.Collect.NeverVisible)
		}
	}
	if !senderHasText(sender, "error persistente de recopilación") {
		t.Fatalf("sent = %+v, want a collection failure alert at the third failure", sender.all())
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.Alerts.CollectionFailure == nil || state.Alerts.CollectionFailure.LastNotifiedAt == nil {
		t.Fatalf("collection failure state = %+v", state.Alerts.CollectionFailure)
	}

	// A fourth failing run is inside the cooldown: no duplicate.
	before := sender.count()
	report := Run(context.Background(), cfg, empty, store, sender, RunOptions{Now: now.Add(4 * time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("cooldown run exit = %d", report.ExitCode)
	}
	if sender.count() != before {
		t.Errorf("cooldown sent %d duplicate messages", sender.count()-before)
	}

	// The VNIC becomes visible: a single recovery follows.
	visible := &fakeMonitoring{handler: fullSeriesHandler(1)}
	report = Run(context.Background(), cfg, visible, store, sender, RunOptions{Now: now.Add(5 * time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("recovery run exit = %d", report.ExitCode)
	}
	if !hasKind(report.Sent, persistence.NotificationCollectionRecovery) {
		t.Fatalf("sent = %+v, want a recovery", report.Sent)
	}
	if strings.Contains(sender.all()[sender.count()-1], "error persistente") {
		t.Error("the recovery run sent a stale error alert")
	}
	state, _ = store.LoadState()
	if state.Alerts.CollectionFailure.FailureSince != nil {
		t.Error("failure_since was not cleared after the recovery")
	}
	if state.ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures = %d, want 0", state.ConsecutiveFailures)
	}
}

func senderHasText(sender *fakeSender, fragment string) bool {
	for _, text := range sender.all() {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func TestRunTelegramDisabledDoesNotEnqueueOrMark(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	cfg.Telegram.Enabled = false
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)

	// An expired queued notification still expires while Telegram is disabled.
	expired := persistence.Notification{
		Kind:      persistence.NotificationAnomaly6H,
		Period:    "2026-10",
		CreatedAt: now.Add(-8 * 24 * time.Hour),
		Text:      "vieja",
	}
	seedNotification(t, store, expired)

	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}
	sender := &fakeSender{}
	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	if sender.count() != 0 {
		t.Errorf("sent = %d, want 0 while disabled", sender.count())
	}
	if report.Skipped == 0 {
		t.Error("Skipped = 0, want the evaluated alert to be logged as skipped")
	}
	if report.Expired != 1 {
		t.Errorf("expired = %d, want 1", report.Expired)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if len(state.Pending) != 0 {
		t.Errorf("pending = %+v, want the expired entry gone", state.Pending)
	}
	if len(state.Alerts.MonthlyAlerts) != 0 {
		t.Errorf("monthly alerts = %+v, want nothing marked", state.Alerts.MonthlyAlerts)
	}
	if state.TelegramEnabled == nil || *state.TelegramEnabled {
		t.Errorf("telegram_enabled = %v, want false", state.TelegramEnabled)
	}
}

func TestRunEnablingTelegramCollapsesStaleQueueIntoOneCurrentAlert(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	cfg.Telegram.Enabled = false
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}
	sender := &fakeSender{}

	// First run with Telegram disabled records the disabled state.
	Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})

	// Add a stale monthly alert as if it had been queued before disabling.
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	state.Pending = append(state.Pending, persistence.Notification{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     50,
		CreatedAt: now.Add(-time.Hour),
		Text:      "alerta mensual antigua del 50 %",
	})
	if err := store.SaveState(state); err != nil {
		t.Fatal(err)
	}

	cfg.Telegram.Enabled = true
	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now.Add(2 * time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want one collapsed current alert:\n%s", sender.count(), strings.Join(sender.all(), "\n---\n"))
	}
	if !strings.Contains(sender.all()[0], "alerta mensual") {
		t.Errorf("unexpected message:\n%s", sender.all()[0])
	}
	if strings.Contains(sender.all()[0], "alerta mensual antigua del 50 %") {
		t.Error("the stale queued message was delivered instead of being collapsed")
	}
	state, _ = store.LoadState()
	if len(state.Pending) != 0 {
		t.Errorf("pending = %+v", state.Pending)
	}
	if state.TelegramEnabled == nil || !*state.TelegramEnabled {
		t.Errorf("telegram_enabled = %v, want true", state.TelegramEnabled)
	}
}

func TestRunQueueFullDropsOldestAndExits5(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)

	notifications := make([]persistence.Notification, 0, 50)
	for i := 0; i < 50; i++ {
		notifications = append(notifications, persistence.Notification{
			Kind:      persistence.NotificationAnomaly6H,
			Period:    fmt.Sprintf("%d-%02d", 2020+i/12, (i%12)+1),
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
			Text:      fmt.Sprintf("pendiente %d", i),
		})
	}
	seedNotification(t, store, notifications...)

	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}
	sender := &fakeSender{sendErr: errors.New("telegram down")}
	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitTelegram {
		t.Fatalf("exit = %d, want 5", report.ExitCode)
	}
	if report.Dropped != 1 {
		t.Errorf("dropped = %d, want 1", report.Dropped)
	}
	state, _ := store.LoadState()
	if len(state.Pending) > 50 {
		t.Errorf("pending = %d, want at most 50", len(state.Pending))
	}
}

func TestRunPendingPreviousMonthGetsNotice(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-10",
		Level:     75,
		CreatedAt: utcHour(2026, 11, 2, 12),
		Text:      "cuerpo de la alerta de octubre",
	})
	client := &fakeMonitoring{handler: fullSeriesHandler(1)}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d", report.ExitCode)
	}
	if sender.count() != 1 {
		t.Fatalf("sent = %d, want 1", sender.count())
	}
	text := sender.all()[0]
	if !strings.Contains(text, "período 2026-10") {
		t.Errorf("missing the original period:\n%s", text)
	}
	if !strings.Contains(text, "no corresponde al mes en curso (2026-11)") {
		t.Errorf("missing the previous-period notice:\n%s", text)
	}
	if !strings.Contains(text, "cuerpo de la alerta de octubre") {
		t.Errorf("missing the stored body:\n%s", text)
	}
}

func TestRunPersistsStateAfterEachSuccessfulSend(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store,
		persistence.Notification{
			Kind: persistence.NotificationMonthlyThreshold, Period: "2026-11", Level: 50,
			CreatedAt: now.Add(-2 * time.Hour), Text: "primera",
		},
		persistence.Notification{
			Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
			CreatedAt: now.Add(-time.Hour), Text: "segunda",
		},
	)

	var seen []int
	sender := &fakeSender{}
	sender.onSend = func(string) error {
		state, err := store.LoadState()
		if err != nil {
			return err
		}
		seen = append(seen, len(state.Pending))
		return nil
	}
	client := &fakeMonitoring{handler: fullSeriesHandler(1)}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	if len(seen) != 2 {
		t.Fatalf("seen = %v, want two sends", seen)
	}
	if seen[0] != 2 {
		t.Errorf("first send saw %d pending, want 2", seen[0])
	}
	if seen[1] != 1 {
		t.Errorf("second send saw %d pending, want 1 (the first delivery must be persisted)", seen[1])
	}
}

func TestRun429LongLeavesNotificationQueuedAndExits5(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
		CreatedAt: now.Add(-time.Hour), Text: "pendiente",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":60}}`)
	}))
	defer server.Close()
	client, err := telegram.New(telegram.Credentials{BotToken: "123:secret", ChatID: "42"}, telegram.Options{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ociClient := &fakeMonitoring{handler: fullSeriesHandler(1)}

	start := time.Now()
	report := Run(context.Background(), cfg, ociClient, store, client, RunOptions{Now: now, Deadline: 5 * time.Second})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the run waited %s for a retry_after that does not fit", elapsed)
	}
	if report.ExitCode != ExitTelegram {
		t.Fatalf("exit = %d, want 5", report.ExitCode)
	}
	var retryErr *telegram.RetryAfterError
	if !errors.As(report.TelegramError, &retryErr) {
		t.Fatalf("error = %v, want *RetryAfterError", report.TelegramError)
	}
	state, _ := store.LoadState()
	if len(state.Pending) != 1 {
		t.Errorf("pending = %+v, want the notification kept", state.Pending)
	}
	if state.Pending[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1", state.Pending[0].Attempts)
	}
	if state.Pending[0].LastAttempt == nil {
		t.Error("last_attempt was not recorded")
	}
}

func TestRunTokenNeverAppearsInLogsOrErrors(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
		CreatedAt: now.Add(-time.Hour), Text: "pendiente",
	})
	const token = "123456:log-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer server.Close()
	client, err := telegram.New(telegram.Credentials{BotToken: token, ChatID: "42"}, telegram.Options{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	logger := &recordingLogger{}
	ociClient := &fakeMonitoring{handler: fullSeriesHandler(1)}

	report := Run(context.Background(), cfg, ociClient, store, client, RunOptions{Now: now, Logger: logger})
	if report.ExitCode != ExitTelegram {
		t.Fatalf("exit = %d, want 5", report.ExitCode)
	}
	if report.TelegramError == nil {
		t.Fatal("TelegramError = nil")
	}
	if strings.Contains(report.TelegramError.Error(), token) {
		t.Errorf("error leaked the token: %v", report.TelegramError)
	}
	if strings.Contains(logger.String(), token) {
		t.Errorf("logs leaked the token:\n%s", logger.String())
	}
}

func TestRunCorruptStateExits4(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(store.StatePath(), "broken"); err != nil {
		t.Fatal(err)
	}
	client := &fakeMonitoring{handler: fullSeriesHandler(1)}
	report := Run(context.Background(), cfg, client, store, &fakeSender{}, RunOptions{Now: utcHour(2026, 11, 7, 13)})
	if report.ExitCode != ExitPersistence {
		t.Fatalf("exit = %d, want 4", report.ExitCode)
	}
	if !errors.Is(report.PersistenceError, persistence.ErrCorrupt) {
		t.Errorf("error = %v, want ErrCorrupt", report.PersistenceError)
	}
}

func TestRunNetworkDisabledReturns0WithoutTouchingState(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.Enabled = false
	store := persistence.New(cfg.Storage.StateDir)
	report := Run(context.Background(), cfg, nil, store, nil, RunOptions{Now: utcHour(2026, 11, 7, 13)})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, want 0", report.ExitCode)
	}
	if report.NetworkEnabled {
		t.Error("NetworkEnabled = true")
	}
	if store.HasState() || store.HasHistory("2026-11") {
		t.Error("a network-disabled run wrote state or history")
	}
	if _, err := os.Stat(store.LockPath()); err != nil {
		t.Errorf("the lock file was not created: %v", err)
	}
}

func TestExitCodePriority(t *testing.T) {
	base := RunReport{ConfigError: errors.New("config"), PersistenceError: errors.New("persist"), OCIError: errors.New("oci"), TelegramError: errors.New("telegram")}
	if got := base.ExitCodeFor(); got != ExitConfig {
		t.Errorf("priority 1 = %d, want 2", got)
	}
	base.ConfigError = nil
	if got := base.ExitCodeFor(); got != ExitPersistence {
		t.Errorf("priority 2 = %d, want 4", got)
	}
	base.PersistenceError = nil
	if got := base.ExitCodeFor(); got != ExitOCI {
		t.Errorf("priority 3 = %d, want 3", got)
	}
	base.OCIError = nil
	if got := base.ExitCodeFor(); got != ExitTelegram {
		t.Errorf("priority 4 = %d, want 5", got)
	}
	base.TelegramError = nil
	if got := base.ExitCodeFor(); got != ExitOK {
		t.Errorf("priority 5 = %d, want 0", got)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func TestPlanCollectionHealthRules(t *testing.T) {
	cfg := runConfig(t)
	now := utcHour(2026, 11, 7, 13)
	histories := map[string]*persistence.History{}

	t.Run("below threshold only records the failure start", func(t *testing.T) {
		state := persistence.NewState()
		state.ConsecutiveFailures = 2
		last := now.Add(-time.Hour)
		state.LastFailure = &last
		alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{})
		if len(alerts) != 0 {
			t.Fatalf("alerts = %+v", alerts)
		}
		if state.Alerts.CollectionFailure == nil || state.Alerts.CollectionFailure.FailureSince == nil {
			t.Fatal("failure_since was not recorded")
		}
	})

	t.Run("threshold proposes one collection failure", func(t *testing.T) {
		state := persistence.NewState()
		state.ConsecutiveFailures = 3
		state.LastFailure = &now
		alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{})
		if len(alerts) != 1 || alerts[0].Notification.Kind != persistence.NotificationCollectionFailure {
			t.Fatalf("alerts = %+v", alerts)
		}
		if !strings.Contains(alerts[0].Text, "error persistente de recopilación") {
			t.Errorf("text = %q", alerts[0].Text)
		}
	})

	t.Run("cooldown suppresses a repetition", func(t *testing.T) {
		state := persistence.NewState()
		state.ConsecutiveFailures = 3
		state.LastFailure = &now
		recent := now.Add(-time.Hour)
		since := now.Add(-48 * time.Hour)
		state.Alerts.CollectionFailure = &persistence.CollectionFailureState{
			FailureSince: &since, LastNotifiedAt: &recent,
		}
		if alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{}); len(alerts) != 0 {
			t.Fatalf("alerts = %+v, want none during cooldown", alerts)
		}
	})

	t.Run("pending failure suppresses a duplicate", func(t *testing.T) {
		state := persistence.NewState()
		state.ConsecutiveFailures = 3
		state.LastFailure = &now
		state.Pending = []persistence.Notification{{
			Kind: persistence.NotificationCollectionFailure, Period: "2026-11",
			CreatedAt: now.Add(-time.Hour),
		}}
		if alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{}); len(alerts) != 0 {
			t.Fatalf("alerts = %+v, want none with a pending failure", alerts)
		}
	})

	t.Run("recovery after a delivered error", func(t *testing.T) {
		state := persistence.NewState()
		since := now.Add(-48 * time.Hour)
		notified := now.Add(-24 * time.Hour)
		last := now.Add(-2 * time.Hour)
		state.LastFailure = &last
		state.Alerts.CollectionFailure = &persistence.CollectionFailureState{
			FailureSince: &since, LastNotifiedAt: &notified,
		}
		alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{})
		if len(alerts) != 1 || alerts[0].Notification.Kind != persistence.NotificationCollectionRecovery {
			t.Fatalf("alerts = %+v", alerts)
		}
		if state.Alerts.CollectionFailure.FailureSince != nil {
			t.Error("failure_since was not cleared")
		}
	})

	t.Run("recovery replaces a queued error", func(t *testing.T) {
		state := persistence.NewState()
		since := now.Add(-48 * time.Hour)
		state.Pending = []persistence.Notification{{
			Kind: persistence.NotificationCollectionFailure, Period: "2026-11",
			CreatedAt: now.Add(-time.Hour),
		}}
		state.Alerts.CollectionFailure = &persistence.CollectionFailureState{FailureSince: &since}
		alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{})
		if len(alerts) != 1 || alerts[0].Notification.Kind != persistence.NotificationCollectionRecovery {
			t.Fatalf("alerts = %+v", alerts)
		}
		if len(state.Pending) != 0 {
			t.Errorf("pending = %+v, want the queued error removed", state.Pending)
		}
	})

	t.Run("recovery never generated is only logged", func(t *testing.T) {
		state := persistence.NewState()
		since := now.Add(-48 * time.Hour)
		state.Alerts.CollectionFailure = &persistence.CollectionFailureState{FailureSince: &since}
		if alerts := planCollectionHealth(&state, cfg, histories, now, discardLogger{}); len(alerts) != 0 {
			t.Fatalf("alerts = %+v, want only a log line", alerts)
		}
		if state.Alerts.CollectionFailure.FailureSince != nil {
			t.Error("failure_since was not cleared")
		}
	})
}

func TestRunMonthlyCollapseSendsHighestAndRecordsCrossed(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.MonthlyLimitGiB = 1
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	// 157 hours * 6 MiB is about 92 % of 1 GiB: crosses 50, 75 and 90 but not 100.
	client := &fakeMonitoring{handler: fullSeriesHandler(6 * 1024 * 1024)}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	if countKind(report.Sent, persistence.NotificationMonthlyThreshold) != 1 {
		t.Fatalf("sent = %+v, want one collapsed monthly alert", report.Sent)
	}
	for _, delivered := range report.Sent {
		if delivered.Kind == persistence.NotificationMonthlyThreshold && delivered.Level != 90 {
			t.Errorf("delivered level = %d, want 90", delivered.Level)
		}
	}
	if text := sender.all()[0]; !strings.Contains(text, "50 %, 75 %, 90 %") {
		t.Errorf("message does not list the crossed thresholds:\n%s", text)
	}
	state, _ := store.LoadState()
	levels := state.Alerts.MonthlyAlerts["2026-11"]
	want := []int{50, 75, 90}
	if len(levels) != len(want) {
		t.Fatalf("monthly alerts = %v, want %v", levels, want)
	}
	for i := range want {
		if levels[i] != want[i] {
			t.Errorf("monthly alerts = %v, want %v", levels, want)
			break
		}
	}

	// The next run must not send the 75 % alert again.
	before := sender.count()
	report = Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now.Add(time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("second exit = %d", report.ExitCode)
	}
	if sender.count() != before {
		t.Errorf("second run sent %d extra messages", sender.count()-before)
	}
}

func TestRunRetriesFailedNotificationOnNextRun(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
		CreatedAt: now.Add(-time.Hour), Text: "pendiente que falla",
	})
	client := &fakeMonitoring{handler: fullSeriesHandler(1)}

	failing := &fakeSender{sendErr: errors.New("telegram down")}
	report := Run(context.Background(), cfg, client, store, failing, RunOptions{Now: now})
	if report.ExitCode != ExitTelegram {
		t.Fatalf("first exit = %d, want 5", report.ExitCode)
	}
	state, _ := store.LoadState()
	if len(state.Pending) != 1 || state.Pending[0].Attempts != 1 {
		t.Fatalf("pending = %+v, want the failed notification with one attempt", state.Pending)
	}

	recovered := &fakeSender{}
	report = Run(context.Background(), cfg, client, store, recovered, RunOptions{Now: now.Add(time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("second exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	if recovered.count() != 1 {
		t.Fatalf("second run sent = %d, want the retry to go out", recovered.count())
	}
	state, _ = store.LoadState()
	if len(state.Pending) != 0 {
		t.Errorf("pending = %+v, want empty after the retry", state.Pending)
	}
}

func TestRunLogsEveryErrorAndKeepsPriority(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
		CreatedAt: now.Add(-time.Hour), Text: "pendiente",
	})
	client := &fakeMonitoring{handler: func(oci.Query) ([]oci.Series, error) {
		return nil, errors.New("oci down")
	}}
	sender := &fakeSender{sendErr: errors.New("telegram down")}
	logger := &recordingLogger{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now, Logger: logger})
	if report.ExitCode != ExitOCI {
		t.Fatalf("exit = %d, want 3", report.ExitCode)
	}
	logs := logger.String()
	if !strings.Contains(logs, "OCI:") {
		t.Errorf("logs do not contain the OCI error:\n%s", logs)
	}
	if !strings.Contains(logs, "telegram:") {
		t.Errorf("logs do not contain the Telegram error:\n%s", logs)
	}
}

func TestDeadlineConstantsReservePersistenceMargin(t *testing.T) {
	if DefaultDeadline != 4*time.Minute {
		t.Errorf("DefaultDeadline = %s, want 4m", DefaultDeadline)
	}
	if DeliveryTimeout >= DefaultDeadline {
		t.Errorf("DeliveryTimeout = %s, want less than the global deadline", DeliveryTimeout)
	}
	if PersistenceMargin >= DefaultDeadline {
		t.Errorf("PersistenceMargin = %s, want less than the global deadline", PersistenceMargin)
	}
}

func TestRunDryRunReportsPendingWithoutTouchingThem(t *testing.T) {
	cfg := runConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	seedNotification(t, store, persistence.Notification{
		Kind: persistence.NotificationAnomaly6H, Period: "2026-11",
		CreatedAt: now.Add(-time.Hour), Text: "pendiente intacta",
	})
	client := &fakeMonitoring{handler: fullSeriesHandler(1)}
	sender := &fakeSender{}

	report := Run(context.Background(), cfg, client, store, sender, RunOptions{Now: now, DryRun: true})
	if report.ExitCode != ExitOK {
		t.Fatalf("exit = %d", report.ExitCode)
	}
	if report.WouldSend != 1 {
		t.Errorf("WouldSend = %d, want the pending notification counted", report.WouldSend)
	}
	if report.Pending != 1 {
		t.Errorf("Pending = %d, want 1", report.Pending)
	}
	if sender.count() != 0 {
		t.Error("dry run sent a message")
	}
	state, _ := store.LoadState()
	if len(state.Pending) != 1 || state.Pending[0].Text != "pendiente intacta" {
		t.Fatalf("pending changed on disk: %+v", state.Pending)
	}
	if state.Pending[0].Attempts != 0 || state.Pending[0].LastAttempt != nil {
		t.Errorf("dry run recorded an attempt: %+v", state.Pending[0])
	}
}

func TestDrainPendingKeepsTelegramErrorWhenSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	cfg := runConfig(t)
	dir := t.TempDir()
	store := persistence.New(dir)
	now := utcHour(2026, 11, 7, 13)
	state := persistence.NewState()
	state.Pending = []persistence.Notification{{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     50,
		CreatedAt: now,
	}}
	sender := &fakeSender{sendErr: errors.New("telegram down")}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)

	_, drain, err := drainPending(context.Background(), cfg, store, sender, state, map[string]*persistence.History{}, now, &recordingLogger{})
	if err == nil {
		t.Fatal("drainPending() save error = nil")
	}
	if !drain.Failed || drain.Err == nil {
		t.Errorf("drain = %+v, want Failed and the Telegram error", drain)
	}
}

func TestRunPersistsAnomalyTextBeforeRetry(t *testing.T) {
	cfg := runConfig(t)
	cfg.Network.HighTraffic6HGiB = 0.001
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13)
	client := &fakeMonitoring{handler: fullSeriesHandler(10 * 1024 * 1024)}

	failing := &fakeSender{sendErr: errors.New("telegram down")}
	report := Run(context.Background(), cfg, client, store, failing, RunOptions{Now: now})
	if report.TelegramError == nil {
		t.Fatal("TelegramError = nil, want the failed anomaly")
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Pending) != 1 {
		t.Fatalf("pending = %+v, want one anomaly", state.Pending)
	}
	if !strings.Contains(state.Pending[0].Text, "consumo anormal") || !strings.Contains(state.Pending[0].Text, "ventana:") {
		t.Fatalf("stored text = %q, want the anomaly window", state.Pending[0].Text)
	}

	recovered := &fakeSender{}
	report = Run(context.Background(), cfg, client, store, recovered, RunOptions{Now: now.Add(time.Hour)})
	if report.ExitCode != ExitOK {
		t.Fatalf("retry exit = %d, errors: %v", report.ExitCode, report.TelegramError)
	}
	texts := recovered.all()
	if len(texts) != 1 || !strings.Contains(texts[0], "consumo anormal") || !strings.Contains(texts[0], "ventana:") {
		t.Fatalf("retried text = %v, want the original anomaly", texts)
	}
}
