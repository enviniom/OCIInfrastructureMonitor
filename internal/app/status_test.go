package app

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func statusConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig()
	cfg.Storage.StateDir = t.TempDir()
	return cfg
}

func TestBuildStatusReport(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	vnic := cfg.Network.VNICOCIDs[0]

	october := persistence.NewHistory("2026-10")
	october.Set(persistence.Bucket{
		ResourceID: vnic, Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 10, 1, 0), Bytes: 500, Status: persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 2, 0),
	})
	october.Finalize(utcHour(2026, 11, 3, 0))
	if err := store.SaveHistory(october); err != nil {
		t.Fatal(err)
	}

	november := persistence.NewHistory("2026-11")
	november.Set(persistence.Bucket{
		ResourceID: vnic, Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 1, 0), Bytes: 300, Status: persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	november.Set(persistence.Bucket{
		ResourceID: vnic, Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 1, 1), Bytes: 0, Status: persistence.StatusNoData,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	if err := store.SaveHistory(november); err != nil {
		t.Fatal(err)
	}

	state := persistence.NewState()
	lastRun := utcHour(2026, 11, 7, 13)
	state.LastRun = &lastRun
	state.LastSuccessfulQuery = &lastRun
	state.AddCorrections("2026-10", 2, -500, lastRun)
	first := utcHour(2026, 10, 1, 0)
	last := utcHour(2026, 11, 5, 12)
	warning := utcHour(2026, 11, 6, 0)
	state.SetVNIC(vnic, persistence.VNICState{
		FirstVisible: &first, LastVisible: &last, Visible: false, WarningSince: &warning,
	})
	if err := store.SaveState(state); err != nil {
		t.Fatal(err)
	}

	report := BuildStatus(store, cfg, utcHour(2026, 11, 7, 13).Add(30*time.Minute))
	if len(report.Errors) != 0 {
		t.Fatalf("errors = %v", report.Errors)
	}
	if !report.StateExists {
		t.Error("StateExists = false")
	}
	if len(report.Months) != 2 {
		t.Fatalf("months = %d, want 2", len(report.Months))
	}

	current := report.Months[0]
	if current.Month != "2026-11" || current.Label != "actual" {
		t.Errorf("current = %+v", current)
	}
	if !current.HasHistory || current.Finalized || current.Grace || current.PendingFinalization {
		t.Errorf("current flags = %+v", current)
	}
	if current.Summary.Outbound.Bytes != 300 {
		t.Errorf("current bytes = %d, want 300", current.Summary.Outbound.Bytes)
	}
	if current.Summary.Outbound.NoData != 1 {
		t.Errorf("current no_data = %d, want 1", current.Summary.Outbound.NoData)
	}

	previous := report.Months[1]
	if previous.Month != "2026-10" || previous.Label != "anterior" {
		t.Errorf("previous = %+v", previous)
	}
	if !previous.Finalized || previous.FinalizedAt == nil {
		t.Errorf("previous finalized flags = %+v", previous)
	}
	if previous.Summary.Outbound.Bytes != 500 {
		t.Errorf("previous bytes = %d, want 500", previous.Summary.Outbound.Bytes)
	}
	if previous.Corrections.BucketsCorrected != 2 || previous.Corrections.DeltaBytes != -500 {
		t.Errorf("corrections = %+v", previous.Corrections)
	}

	if len(report.Warnings) != 1 || report.Warnings[0].OCID != vnic {
		t.Fatalf("warnings = %+v", report.Warnings)
	}
	if report.Warnings[0].LastVisible == nil || !report.Warnings[0].LastVisible.Equal(last) {
		t.Errorf("warning last visible = %v", report.Warnings[0].LastVisible)
	}
}

func TestBuildStatusGraceAndPendingFinalization(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(persistence.NewHistory("2026-10")); err != nil {
		t.Fatal(err)
	}

	inGrace := BuildStatus(store, cfg, utcHour(2026, 11, 1, 12))
	previous := inGrace.Months[1]
	if !previous.Grace || previous.PendingFinalization {
		t.Errorf("grace flags = %+v, want grace and not pending", previous)
	}

	pending := BuildStatus(store, cfg, utcHour(2026, 11, 7, 13))
	previous = pending.Months[1]
	if previous.Grace || !previous.PendingFinalization {
		t.Errorf("pending flags = %+v, want pending and not grace", previous)
	}
}

func TestBuildStatusCorruptHistoryOnlyAffectsThatMonth(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(persistence.NewHistory("2026-11")); err != nil {
		t.Fatal(err)
	}
	path, _ := store.HistoryPath("2026-10")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := BuildStatus(store, cfg, utcHour(2026, 11, 7, 13))
	if len(report.Errors) != 1 {
		t.Fatalf("errors = %v, want 1", report.Errors)
	}
	if report.Months[0].Err != nil {
		t.Errorf("current month error = %v, want nil", report.Months[0].Err)
	}
	if report.Months[1].Err == nil {
		t.Error("previous month error = nil, want a corruption error")
	}
	if !errors.Is(report.Months[1].Err, persistence.ErrCorrupt) {
		t.Errorf("previous month error = %v, want ErrCorrupt", report.Months[1].Err)
	}
}

func TestBuildStatusCorruptStateStillShowsHistory(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(persistence.NewHistory("2026-11")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StatePath(), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := BuildStatus(store, cfg, utcHour(2026, 11, 7, 13))
	if report.StateErr == nil {
		t.Fatal("StateErr = nil")
	}
	if len(report.Errors) != 1 {
		t.Errorf("errors = %v, want 1", report.Errors)
	}
	if !report.StateExists {
		t.Error("StateExists = false")
	}
	if !report.Months[0].HasHistory {
		t.Error("current history was not read")
	}
}

func TestBuildStatusWithoutState(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	report := BuildStatus(store, cfg, utcHour(2026, 11, 7, 13))
	if report.StateExists {
		t.Error("StateExists = true")
	}
	if len(report.Errors) != 0 {
		t.Errorf("errors = %v", report.Errors)
	}
	for _, month := range report.Months {
		if month.HasHistory {
			t.Errorf("month %s has history", month.Month)
		}
		if month.Summary.HasData {
			t.Errorf("month %s has data", month.Month)
		}
	}
	if got := accumulate.Summarize(persistence.NewHistory("2026-11"), optionsFromConfig(cfg)); got.HasData {
		t.Error("empty summary has data")
	}
}
