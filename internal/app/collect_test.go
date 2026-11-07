package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

type fakeMonitoring struct {
	mu      sync.Mutex
	handler func(oci.Query) ([]oci.Series, error)
	calls   []oci.Query
}

func utcHour(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func (f *fakeMonitoring) Summarize(_ context.Context, query oci.Query) ([]oci.Series, error) {
	f.mu.Lock()
	f.calls = append(f.calls, query)
	f.mu.Unlock()
	return f.handler(query)
}

func (f *fakeMonitoring) ListMetrics(context.Context, string, string, map[string]string) ([]oci.MetricDefinition, error) {
	return nil, errors.New("ListMetrics is not expected during collection")
}

func (f *fakeMonitoring) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func parseVNIC(mql string) string {
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

func fullSeries(query oci.Query, value float64) []oci.Series {
	vnic := parseVNIC(query.MQL)
	var points []oci.Point
	for hour := query.StartTime.UTC().Truncate(time.Hour); hour.Before(query.EndTime); hour = hour.Add(time.Hour) {
		points = append(points, oci.Point{Time: hour.Add(time.Hour), Value: value})
	}
	return []oci.Series{{ResourceID: vnic, Points: points}}
}

func fullSeriesHandler(value float64) func(oci.Query) ([]oci.Series, error) {
	return func(query oci.Query) ([]oci.Series, error) {
		return fullSeries(query, value), nil
	}
}

func collectConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig()
	cfg.Network.VNICOCIDs = []string{testOCID("vnic", "b")}
	cfg.Network.CollectInbound = false
	cfg.Storage.StateDir = t.TempDir()
	return cfg
}

func seedHistory(t *testing.T, store *persistence.Store, month, vnic string, start, end time.Time, bytes int64) {
	t.Helper()
	history := persistence.NewHistory(month)
	for hour := start.UTC(); hour.Before(end); hour = hour.Add(time.Hour) {
		history.Set(persistence.Bucket{
			ResourceID: vnic,
			Direction:  persistence.DirectionOutbound,
			Hour:       hour,
			Bytes:      bytes,
			Status:     persistence.StatusComplete,
			CapturedAt: utcHour(2026, 10, 31, 23),
		})
	}
	if err := store.SaveHistory(history); err != nil {
		t.Fatalf("seedHistory(%s): %v", month, err)
	}
}

func TestCollectFirstRun(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}

	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.Queries != 1 || report.Succeeded != 1 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}

	history, exists, err := store.LoadHistory("2026-11")
	if err != nil || !exists {
		t.Fatalf("LoadHistory() exists=%v error=%v", exists, err)
	}
	if history.Len() != 157 {
		t.Errorf("buckets = %d, want 157", history.Len())
	}

	summary := report.Summaries["2026-11"]
	if summary.Outbound.Bytes != 157000 {
		t.Errorf("Bytes = %d, want 157000", summary.Outbound.Bytes)
	}
	if summary.Outbound.Complete != 155 || summary.Outbound.Provisional != 2 {
		t.Errorf("counts = %+v, want 155 complete and 2 provisional", summary.Outbound)
	}
	if summary.Status != persistence.StatusProvisional {
		t.Errorf("Status = %s, want provisional", summary.Status)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.LastRun == nil || !state.LastRun.Equal(now) {
		t.Errorf("LastRun = %v, want %s", state.LastRun, now)
	}
	if state.LastSuccessfulQuery == nil || !state.LastSuccessfulQuery.Equal(now) {
		t.Errorf("LastSuccessfulQuery = %v, want %s", state.LastSuccessfulQuery, now)
	}
	if state.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", state.ConsecutiveFailures)
	}
	vnic := state.VNIC(cfg.Network.VNICOCIDs[0])
	if !vnic.Visible {
		t.Error("VNIC not visible")
	}
	if vnic.FirstVisible == nil || !vnic.FirstVisible.Equal(utcHour(2026, 11, 1, 0)) {
		t.Errorf("FirstVisible = %v, want 2026-11-01 00:00", vnic.FirstVisible)
	}
	if vnic.LastVisible == nil || !vnic.LastVisible.Equal(utcHour(2026, 11, 7, 12)) {
		t.Errorf("LastVisible = %v, want 2026-11-07 12:00", vnic.LastVisible)
	}
}

func TestCollectRerunDoesNotDoubleCount(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}

	if _, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now}); err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	if report.Queries != 1 {
		t.Errorf("second run queries = %d, want 1", report.Queries)
	}
	if stats := report.Corrections["2026-11"]; stats.Buckets != 0 {
		t.Errorf("corrections = %+v, want none", stats)
	}

	history, _, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if history.Len() != 157 {
		t.Errorf("buckets = %d, want 157 after two runs", history.Len())
	}
	summary := accumulate.Summarize(history, optionsFromConfig(cfg))
	if summary.Outbound.Bytes != 157000 {
		t.Errorf("Bytes = %d, want 157000 (no duplicate bytes)", summary.Outbound.Bytes)
	}
}

func TestCollectReconcilesFinalizedHistoryIntoState(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	history := persistence.NewHistory("2026-10")
	history.Set(persistence.Bucket{
		ResourceID: cfg.Network.VNICOCIDs[0],
		Direction:  persistence.DirectionOutbound,
		Hour:       utcHour(2026, 10, 1, 0),
		Bytes:      10,
		Status:     persistence.StatusComplete,
		CapturedAt: utcHour(2026, 10, 31, 23),
	})
	history.Finalize(utcHour(2026, 11, 3, 0))
	if err := store.SaveHistory(history); err != nil {
		t.Fatal(err)
	}

	now := utcHour(2026, 11, 7, 13)
	client := &fakeMonitoring{handler: fullSeriesHandler(100)}
	if _, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now}); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsFinalized("2026-10") {
		t.Error("state did not adopt the finalized history")
	}
	for _, call := range client.calls {
		if call.StartTime.Before(utcHour(2026, 11, 1, 0)) {
			t.Errorf("finalized history was queried: %s", call.StartTime)
		}
	}
}

func TestCollectMonthChangeFinalizesPreviousMonth(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(100)}

	octoberRun := utcHour(2026, 10, 31, 23).Add(30 * time.Minute)
	if _, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: octoberRun}); err != nil {
		t.Fatalf("October Collect() error = %v", err)
	}
	novemberRun := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: novemberRun})
	if err != nil {
		t.Fatalf("November Collect() error = %v", err)
	}
	if len(report.Finalized) != 1 || report.Finalized[0] != "2026-10" {
		t.Fatalf("Finalized = %v, want [2026-10]", report.Finalized)
	}

	october, _, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if !october.Finalized {
		t.Error("October is not finalized")
	}
	if october.Len() != 744 {
		t.Errorf("October buckets = %d, want 744", october.Len())
	}
	summary := accumulate.Summarize(october, optionsFromConfig(cfg))
	if summary.Outbound.Bytes != 74400 {
		t.Errorf("October bytes = %d, want 74400", summary.Outbound.Bytes)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsFinalized("2026-10") {
		t.Error("state does not remember the finalized month")
	}

	// A later run must not query or modify the finalized month.
	laterRun := novemberRun.Add(time.Hour)
	later := &fakeMonitoring{handler: fullSeriesHandler(999)}
	if _, err := Collect(context.Background(), cfg, later, store, CollectOptions{Now: laterRun}); err != nil {
		t.Fatalf("later Collect() error = %v", err)
	}
	for _, call := range later.calls {
		if call.StartTime.Before(utcHour(2026, 11, 1, 0)) {
			t.Errorf("finalized month was queried: window starts at %s", call.StartTime)
		}
	}
	after, _, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if got := accumulate.Summarize(after, optionsFromConfig(cfg)).Outbound.Bytes; got != 74400 {
		t.Errorf("finalized month changed to %d bytes", got)
	}
}

func TestCollectMonthWithoutRunsIsRecovered(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	vnic := cfg.Network.VNICOCIDs[0]

	seedHistory(t, store, "2026-10", vnic, utcHour(2026, 10, 1, 0), utcHour(2026, 10, 30, 12), 100)
	state := persistence.NewState()
	trace := utcHour(2026, 10, 30, 12)
	state.LastRun = &trace
	state.LastSuccessfulQuery = &trace
	if err := store.SaveState(state); err != nil {
		t.Fatal(err)
	}

	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(100)}
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(report.Finalized) != 1 || report.Finalized[0] != "2026-10" {
		t.Fatalf("Finalized = %v, want October", report.Finalized)
	}
	october, _, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if october.Len() != 744 {
		t.Errorf("October buckets = %d, want 744 recovered from OCI", october.Len())
	}
	if !october.Finalized {
		t.Error("October not finalized")
	}
}

func TestCollectRecoversAfterOutage(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	vnic := cfg.Network.VNICOCIDs[0]

	seedHistory(t, store, "2026-10", vnic, utcHour(2026, 10, 1, 0), utcHour(2026, 10, 20, 12), 100)
	state := persistence.NewState()
	trace := utcHour(2026, 10, 20, 12)
	state.LastRun = &trace
	state.LastSuccessfulQuery = &trace
	if err := store.SaveState(state); err != nil {
		t.Fatal(err)
	}

	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(100)}
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.Queries != 2 {
		t.Errorf("queries = %d, want 2 (October finalization and November)", report.Queries)
	}
	october, _, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if october.Len() != 744 || !october.Finalized {
		t.Errorf("October = %d buckets, finalized %v; want 744 finalized", october.Len(), october.Finalized)
	}
	november, _, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if november.Len() != 157 {
		t.Errorf("November buckets = %d, want 157", november.Len())
	}
}

func TestCollectMarksUnrecoverableGaps(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	state := persistence.NewState()
	trace := utcHour(2026, 7, 30, 12)
	state.LastRun = &trace
	state.LastSuccessfulQuery = &trace
	if err := store.SaveState(state); err != nil {
		t.Fatal(err)
	}

	client := &fakeMonitoring{handler: fullSeriesHandler(100)}
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(report.Finalized) != 3 {
		t.Errorf("Finalized = %v, want August, September and October", report.Finalized)
	}
	if report.GapsFilled != 205 {
		t.Errorf("GapsFilled = %d, want 205 hours out of retention", report.GapsFilled)
	}

	august, _, err := store.LoadHistory("2026-08")
	if err != nil {
		t.Fatal(err)
	}
	if !august.Finalized {
		t.Error("August not finalized")
	}
	summary := accumulate.Summarize(august, optionsFromConfig(cfg))
	if summary.Outbound.Incomplete != 205 {
		t.Errorf("August incomplete = %d, want 205", summary.Outbound.Incomplete)
	}
	if summary.Status != persistence.StatusIncomplete {
		t.Errorf("August status = %s, want incomplete", summary.Status)
	}
}

func TestCollectPartialVNICFailure(t *testing.T) {
	cfg := collectConfig(t)
	cfg.Network.VNICOCIDs = []string{testOCID("vnic", "b"), testOCID("vnic", "d")}
	store := persistence.New(cfg.Storage.StateDir)
	failing := cfg.Network.VNICOCIDs[1]
	client := &fakeMonitoring{handler: func(query oci.Query) ([]oci.Series, error) {
		if strings.Contains(query.MQL, failing) {
			return nil, errors.New("denied")
		}
		return fullSeries(query, 1000), nil
	}}

	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.Queries != 2 || report.Succeeded != 1 || len(report.Failures) != 1 {
		t.Fatalf("report = queries %d, succeeded %d, failures %d", report.Queries, report.Succeeded, len(report.Failures))
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.ConsecutiveFailures != 1 || state.LastFailure == nil {
		t.Errorf("failure counters = %d, %v", state.ConsecutiveFailures, state.LastFailure)
	}

	history, _, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range history.Buckets {
		if bucket.ResourceID == failing && bucket.Status != persistence.StatusIncomplete {
			t.Fatalf("failing VNIC bucket = %s, want incomplete", bucket.Status)
		}
	}
	summary := accumulate.Summarize(history, optionsFromConfig(cfg))
	if summary.Status != persistence.StatusIncomplete {
		t.Errorf("total status = %s, want incomplete", summary.Status)
	}
}

func TestCollectCorrectionsDuringGrace(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: fullSeriesHandler(100)}

	octoberRun := utcHour(2026, 10, 31, 23).Add(30 * time.Minute)
	if _, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: octoberRun}); err != nil {
		t.Fatalf("October Collect() error = %v", err)
	}

	corrected := &fakeMonitoring{handler: func(query oci.Query) ([]oci.Series, error) {
		series := fullSeries(query, 100)
		for i := range series[0].Points {
			if series[0].Points[i].Time.Equal(utcHour(2026, 10, 30, 14)) {
				series[0].Points[i].Value = 200
			}
		}
		return series, nil
	}}
	graceRun := utcHour(2026, 11, 1, 12)
	report, err := Collect(context.Background(), cfg, corrected, store, CollectOptions{Now: graceRun})
	if err != nil {
		t.Fatalf("grace Collect() error = %v", err)
	}
	stats := report.Corrections["2026-10"]
	if stats.Buckets != 1 || stats.DeltaBytes != 100 {
		t.Errorf("corrections = %+v, want 1 bucket and +100 bytes", stats)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Corrections("2026-10"); got.BucketsCorrected != 1 || got.DeltaBytes != 100 {
		t.Errorf("state corrections = %+v", got)
	}
	october, _, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if october.Finalized {
		t.Error("October finalized inside the grace period")
	}
}

func TestCollectDryRunWritesNothing(t *testing.T) {
	cfg := collectConfig(t)
	cfg.Storage.StateDir = filepath.Join(t.TempDir(), "state")
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}

	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now, DryRun: true})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !report.DryRun {
		t.Error("report.DryRun = false")
	}
	if report.Queries != 1 {
		t.Errorf("queries = %d, want 1", report.Queries)
	}
	if _, err := os.Stat(cfg.Storage.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state directory was created during a dry run: %v", err)
	}
}

func TestCollectWarnsWhenVNICDisappears(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	withData := &fakeMonitoring{handler: fullSeriesHandler(1000)}
	if _, err := Collect(context.Background(), cfg, withData, store, CollectOptions{Now: now}); err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}

	later := now.Add(2 * time.Hour)
	withoutData := &fakeMonitoring{handler: func(oci.Query) ([]oci.Series, error) {
		return nil, nil
	}}
	report, err := Collect(context.Background(), cfg, withoutData, store, CollectOptions{Now: later})
	if err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != cfg.Network.VNICOCIDs[0] {
		t.Fatalf("warnings = %v", report.Warnings)
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	vnic := state.VNIC(cfg.Network.VNICOCIDs[0])
	if vnic.Visible {
		t.Error("VNIC still visible")
	}
	if vnic.WarningSince == nil || !vnic.WarningSince.Equal(later) {
		t.Errorf("WarningSince = %v, want %s", vnic.WarningSince, later)
	}
	if vnic.LastVisible == nil {
		t.Error("LastVisible was lost")
	}

	// Confirmed hours are not erased by a response that omits them.
	history, _, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	summary := accumulate.Summarize(history, optionsFromConfig(cfg))
	if summary.Outbound.Bytes != 157000 {
		t.Errorf("bytes = %d, want 157000", summary.Outbound.Bytes)
	}
}

func TestCollectCorruptStateFails(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StatePath(), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}
	_, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: utcHour(2026, 11, 7, 13)})
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("Collect() error = %v, want ErrCorrupt", err)
	}
	if _, err := os.Stat(store.StatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("corrupt state still in place: %v", err)
	}
	months, err := store.ListMonths()
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 0 {
		t.Errorf("history written after a corrupt state: %v", months)
	}
}

func TestCollectDryRunDoesNotQuarantine(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	const content = "{oops"
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}
	_, err := Collect(context.Background(), cfg, client, store, CollectOptions{
		Now:    utcHour(2026, 11, 7, 13),
		DryRun: true,
	})
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("Collect() error = %v, want ErrCorrupt", err)
	}
	got, readErr := os.ReadFile(store.StatePath())
	if readErr != nil {
		t.Fatalf("dry run moved the corrupt state: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("state content changed to %q", got)
	}
}

func TestCollectUnknownSchemaFailsWithoutOverwrite(t *testing.T) {
	cfg := collectConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	const content = `{"schema_version": 7, "future": true}`
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &fakeMonitoring{handler: fullSeriesHandler(1000)}
	_, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: utcHour(2026, 11, 7, 13)})
	if !errors.Is(err, persistence.ErrSchemaVersion) {
		t.Fatalf("Collect() error = %v, want ErrSchemaVersion", err)
	}
	got, readErr := os.ReadFile(store.StatePath())
	if readErr != nil {
		t.Fatalf("state file moved: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("state overwritten: %q", got)
	}
}

func TestCollectNetworkDisabledDoesNothing(t *testing.T) {
	cfg := collectConfig(t)
	cfg.Network.Enabled = false
	store := persistence.New(cfg.Storage.StateDir)

	report, err := Collect(context.Background(), cfg, nil, store, CollectOptions{Now: utcHour(2026, 11, 7, 13)})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.Queries != 0 {
		t.Errorf("queries = %d, want 0", report.Queries)
	}
	if store.HasState() {
		t.Error("state written with network disabled")
	}
}

func TestCollectInboundStaysSeparate(t *testing.T) {
	cfg := collectConfig(t)
	cfg.Network.CollectInbound = true
	store := persistence.New(cfg.Storage.StateDir)
	client := &fakeMonitoring{handler: func(query oci.Query) ([]oci.Series, error) {
		if strings.Contains(query.MQL, accumulate.MetricInbound) {
			return fullSeries(query, 5000), nil
		}
		return fullSeries(query, 1000), nil
	}}

	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	report, err := Collect(context.Background(), cfg, client, store, CollectOptions{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.Queries != 2 {
		t.Fatalf("queries = %d, want 2", report.Queries)
	}
	summary := report.Summaries["2026-11"]
	if summary.Outbound.Bytes != 157000 {
		t.Errorf("outbound bytes = %d, want 157000", summary.Outbound.Bytes)
	}
	if summary.Inbound.Bytes != 785000 {
		t.Errorf("inbound bytes = %d, want 785000", summary.Inbound.Bytes)
	}
	if summary.Status != persistence.StatusProvisional {
		t.Errorf("status = %s, want provisional", summary.Status)
	}
}
