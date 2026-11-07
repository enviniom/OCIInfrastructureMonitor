package accumulate

import (
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func addBucket(history *persistence.History, vnic string, direction persistence.Direction, hour time.Time, bytes int64, status persistence.Status) {
	history.Set(persistence.Bucket{
		ResourceID: vnic,
		Direction:  direction,
		Hour:       hour,
		Bytes:      bytes,
		Status:     status,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
}

func TestSummarizeTotalsAndCounts(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 0), 100, persistence.StatusComplete)
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 1), 200, persistence.StatusComplete)
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 2), 0, persistence.StatusNoData)
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 3), 50, persistence.StatusProvisional)

	summary := Summarize(history, testOptions())
	if !summary.HasData {
		t.Fatal("HasData = false")
	}
	if summary.Outbound.Bytes != 350 {
		t.Errorf("Bytes = %d, want 350", summary.Outbound.Bytes)
	}
	if summary.Outbound.Buckets != 4 {
		t.Errorf("Buckets = %d, want 4", summary.Outbound.Buckets)
	}
	if summary.Outbound.Complete != 2 || summary.Outbound.NoData != 1 || summary.Outbound.Provisional != 1 || summary.Outbound.Incomplete != 0 {
		t.Errorf("counts = %+v", summary.Outbound)
	}
	if summary.Status != persistence.StatusProvisional {
		t.Errorf("Status = %s, want provisional", summary.Status)
	}
}

func TestSummarizeIncompleteWins(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 0), 100, persistence.StatusComplete)
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 1), 0, persistence.StatusIncomplete)

	summary := Summarize(history, testOptions())
	if summary.Status != persistence.StatusIncomplete {
		t.Errorf("Status = %s, want incomplete", summary.Status)
	}
	if summary.Outbound.Reason == "" {
		t.Error("Reason is empty for an incomplete total")
	}
}

func TestSummarizeNoDataStreak(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	for hour := utcHour(2026, 11, 1, 0); hour.Before(utcHour(2026, 11, 1, 7)); hour = hour.Add(time.Hour) {
		addBucket(history, "vnic-a", persistence.DirectionOutbound, hour, 0, persistence.StatusNoData)
	}

	opts := testOptions()
	summary := Summarize(history, opts)
	if summary.Outbound.MaxNoDataStreak != 7 {
		t.Errorf("MaxNoDataStreak = %d, want 7", summary.Outbound.MaxNoDataStreak)
	}
	if summary.Status != persistence.StatusComplete {
		t.Errorf("Status without explicit zeros = %s, want complete", summary.Status)
	}

	opts.PublishZeros = true
	summary = Summarize(history, opts)
	if summary.Status != persistence.StatusIncomplete {
		t.Errorf("Status with explicit zeros = %s, want incomplete", summary.Status)
	}
	if summary.Outbound.Reason == "" {
		t.Error("Reason is empty for a no_data streak")
	}
}

func TestSummarizeStreakIsPerResource(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	for hour := utcHour(2026, 11, 1, 0); hour.Before(utcHour(2026, 11, 1, 4)); hour = hour.Add(time.Hour) {
		addBucket(history, "vnic-a", persistence.DirectionOutbound, hour, 0, persistence.StatusNoData)
		addBucket(history, "vnic-b", persistence.DirectionOutbound, hour, 0, persistence.StatusNoData)
	}

	summary := Summarize(history, testOptions())
	if summary.Outbound.MaxNoDataStreak != 4 {
		t.Errorf("MaxNoDataStreak = %d, want 4 per resource", summary.Outbound.MaxNoDataStreak)
	}
}

func TestSummarizeEmptyMonth(t *testing.T) {
	summary := Summarize(persistence.NewHistory("2026-11"), testOptions())
	if summary.HasData {
		t.Error("HasData = true")
	}
	if summary.Status != persistence.StatusIncomplete {
		t.Errorf("Status = %s, want incomplete for an empty month", summary.Status)
	}
	if summary.Outbound.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0", summary.Outbound.Bytes)
	}
}

func TestSummarizeInboundIsSeparate(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	addBucket(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 0), 100, persistence.StatusComplete)
	addBucket(history, "vnic-a", persistence.DirectionInbound, utcHour(2026, 11, 1, 0), 500, persistence.StatusComplete)

	summary := Summarize(history, testOptions())
	if summary.Outbound.Bytes != 100 {
		t.Errorf("Outbound.Bytes = %d, want 100", summary.Outbound.Bytes)
	}
	if summary.Inbound.Bytes != 500 {
		t.Errorf("Inbound.Bytes = %d, want 500", summary.Inbound.Bytes)
	}
	if summary.Status != persistence.StatusComplete {
		t.Errorf("Status = %s, want complete", summary.Status)
	}
}

func TestSummarizeFinalizedFlags(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	history.Finalize(utcHour(2026, 11, 3, 0))
	summary := Summarize(history, testOptions())
	if !summary.Finalized || summary.FinalizedAt == nil {
		t.Errorf("summary = %+v, want finalized with a date", summary)
	}
}
