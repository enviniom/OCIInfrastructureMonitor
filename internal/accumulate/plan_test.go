package accumulate

import (
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func fillComplete(history *persistence.History, vnic string, direction persistence.Direction, start, end time.Time, bytes int64) {
	for hour := start.UTC(); hour.Before(end); hour = hour.Add(time.Hour) {
		history.Set(persistence.Bucket{
			ResourceID: vnic,
			Direction:  direction,
			Hour:       hour,
			Bytes:      bytes,
			Status:     persistence.StatusComplete,
			CapturedAt: end,
		})
	}
}

func stateWithTrace(t *testing.T, at time.Time) persistence.State {
	t.Helper()
	state := persistence.NewState()
	trace := at.UTC()
	state.LastRun = &trace
	state.LastSuccessfulQuery = &trace
	return state
}

func TestMonthsToConsider(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)

	fresh := persistence.NewState()
	if months := MonthsToConsider(now, fresh, testOptions(), nil); len(months) != 1 || months[0] != "2026-11" {
		t.Errorf("fresh months = %v, want [2026-11]", months)
	}

	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))
	if months := MonthsToConsider(now, state, testOptions(), nil); len(months) != 2 || months[0] != "2026-10" || months[1] != "2026-11" {
		t.Errorf("months = %v, want [2026-10 2026-11]", months)
	}

	state = stateWithTrace(t, utcHour(2026, 8, 20, 12))
	months := MonthsToConsider(now, state, testOptions(), nil)
	want := []string{"2026-08", "2026-09", "2026-10", "2026-11"}
	if len(months) != len(want) {
		t.Fatalf("months = %v, want %v", months, want)
	}
	for i := range want {
		if months[i] != want[i] {
			t.Errorf("months[%d] = %q, want %q", i, months[i], want[i])
		}
	}

	// A trace older than the retention is clamped to the retention horizon.
	state = stateWithTrace(t, utcHour(2024, 1, 1, 0))
	months = MonthsToConsider(now, state, testOptions(), nil)
	if len(months) != 4 || months[0] != "2026-08" || months[3] != "2026-11" {
		t.Errorf("clamped months = %v, want 2026-08..2026-11", months)
	}

	// A trace in the current month does not pull in the previous month unless
	// that month already has a history file.
	state = stateWithTrace(t, utcHour(2026, 11, 6, 12))
	if months := MonthsToConsider(now, state, testOptions(), nil); len(months) != 1 || months[0] != "2026-11" {
		t.Errorf("months = %v, want [2026-11]", months)
	}
	if months := MonthsToConsider(now, state, testOptions(), []string{"2026-10"}); len(months) != 2 || months[0] != "2026-10" {
		t.Errorf("months with history = %v, want [2026-10 2026-11]", months)
	}

	// A shorter retention moves the horizon and clamps an old trace.
	short := testOptions()
	short.Retention = 10 * 24 * time.Hour
	state = stateWithTrace(t, utcHour(2026, 8, 20, 12))
	if months := MonthsToConsider(now, state, short, nil); len(months) != 2 || months[0] != "2026-10" {
		t.Errorf("short retention months = %v, want [2026-10 2026-11]", months)
	}

	// A trace in the future (clock skew) must not skip the current month.
	state = stateWithTrace(t, utcHour(2026, 12, 1, 0))
	if months := MonthsToConsider(now, state, testOptions(), nil); len(months) != 1 || months[0] != "2026-11" {
		t.Errorf("future trace months = %v, want [2026-11]", months)
	}

	// A history file alone, without any trace, still pulls its month in.
	if months := MonthsToConsider(now, persistence.NewState(), testOptions(), []string{"2026-09", "2026-10"}); len(months) != 3 || months[0] != "2026-09" {
		t.Errorf("months with history and no trace = %v, want 2026-09..2026-11", months)
	}
}

func TestPlanFreshInstallQueriesCurrentMonth(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	opts := testOptions()
	plan := PlanMonths(now, opts, persistence.NewState(), map[string]*persistence.History{})

	if len(plan.Months) != 1 {
		t.Fatalf("months = %d, want 1", len(plan.Months))
	}
	month := plan.Months[0]
	if month.Month != "2026-11" || !month.Current || month.Due {
		t.Fatalf("month = %+v", month)
	}
	if len(month.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(month.Jobs))
	}
	job := month.Jobs[0]
	if !job.Start.Equal(utcHour(2026, 11, 1, 0)) || !job.End.Equal(utcHour(2026, 11, 7, 13)) {
		t.Errorf("job window = %s..%s, want 2026-11-01 00:00..2026-11-07 13:00", job.Start, job.End)
	}
	if job.Finalization {
		t.Error("fresh install job is a finalization")
	}
}

func TestPlanRequeryWindowIsAtLeast48Hours(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	opts := testOptions()
	history := persistence.NewHistory("2026-11")
	fillComplete(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 0), utcHour(2026, 11, 7, 10), 100)

	plan := PlanMonths(now, opts, persistence.NewState(), map[string]*persistence.History{"2026-11": history})
	job := plan.Months[0].Jobs[0]
	if !job.Start.Equal(utcHour(2026, 11, 5, 13)) {
		t.Errorf("job start = %s, want 2026-11-05 13:00 (now - 48 h)", job.Start)
	}
}

func TestPlanExtendsToOldestAttention(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	opts := testOptions()
	history := persistence.NewHistory("2026-11")
	for hour := utcHour(2026, 11, 1, 0); hour.Before(utcHour(2026, 11, 7, 10)); hour = hour.Add(time.Hour) {
		if hour.Equal(utcHour(2026, 11, 3, 5)) {
			continue
		}
		history.Set(persistence.Bucket{
			ResourceID: "vnic-a",
			Direction:  persistence.DirectionOutbound,
			Hour:       hour,
			Bytes:      100,
			Status:     persistence.StatusComplete,
			CapturedAt: now,
		})
	}

	plan := PlanMonths(now, opts, persistence.NewState(), map[string]*persistence.History{"2026-11": history})
	job := plan.Months[0].Jobs[0]
	if !job.Start.Equal(utcHour(2026, 11, 3, 5)) {
		t.Errorf("job start = %s, want the oldest missing hour 2026-11-03 05:00", job.Start)
	}
}

func TestPlanGracePeriodRequeresPreviousMonth(t *testing.T) {
	now := utcHour(2026, 11, 2, 0)
	opts := testOptions()
	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))

	plan := PlanMonths(now, opts, state, map[string]*persistence.History{})
	var october *MonthPlan
	for i := range plan.Months {
		if plan.Months[i].Month == "2026-10" {
			october = &plan.Months[i]
		}
	}
	if october == nil {
		t.Fatal("2026-10 not planned")
	}
	if october.Due {
		t.Error("October is due before the grace period ends")
	}
	if len(october.Jobs) != 1 {
		t.Fatalf("October jobs = %d, want 1", len(october.Jobs))
	}
	job := october.Jobs[0]
	if job.Finalization {
		t.Error("grace job is a finalization")
	}
	if !job.Start.Equal(utcHour(2026, 10, 31, 0)) || !job.End.Equal(utcHour(2026, 11, 1, 0)) {
		t.Errorf("job window = %s..%s, want 2026-10-31 00:00..2026-11-01 00:00", job.Start, job.End)
	}
}

func TestPlanFinalizationCoversToMonthEnd(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	opts := testOptions()
	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))

	plan := PlanMonths(now, opts, state, map[string]*persistence.History{})
	var october *MonthPlan
	for i := range plan.Months {
		if plan.Months[i].Month == "2026-10" {
			october = &plan.Months[i]
		}
	}
	if october == nil {
		t.Fatal("2026-10 not planned")
	}
	if !october.Due {
		t.Error("October is not due after the grace period")
	}
	if len(october.Jobs) != 1 {
		t.Fatalf("October jobs = %d, want 1", len(october.Jobs))
	}
	job := october.Jobs[0]
	if !job.Finalization {
		t.Error("October job is not a finalization")
	}
	if !job.Start.Equal(utcHour(2026, 10, 1, 0)) || !job.End.Equal(utcHour(2026, 11, 1, 0)) {
		t.Errorf("job window = %s..%s, want 2026-10-01 00:00..2026-11-01 00:00", job.Start, job.End)
	}
}

func TestPlanFinalizationWithoutAttentionHasNoJob(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)
	opts := testOptions()
	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))
	history := persistence.NewHistory("2026-10")
	fillComplete(history, "vnic-a", persistence.DirectionOutbound, utcHour(2026, 10, 1, 0), utcHour(2026, 11, 1, 0), 100)

	plan := PlanMonths(now, opts, state, map[string]*persistence.History{"2026-10": history})
	var october *MonthPlan
	for i := range plan.Months {
		if plan.Months[i].Month == "2026-10" {
			october = &plan.Months[i]
		}
	}
	if october == nil {
		t.Fatal("2026-10 not planned")
	}
	if !october.Due {
		t.Error("October is not due")
	}
	if len(october.Jobs) != 0 {
		t.Errorf("October jobs = %d, want 0", len(october.Jobs))
	}
}

func TestPlanPartialRetentionMarkGapsAndQueryTheRest(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	opts := testOptions()
	opts.Retention = 10 * 24 * time.Hour
	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))

	plan := PlanMonths(now, opts, state, map[string]*persistence.History{})
	var october *MonthPlan
	for i := range plan.Months {
		if plan.Months[i].Month == "2026-10" {
			october = &plan.Months[i]
		}
	}
	if october == nil {
		t.Fatal("2026-10 not planned")
	}
	if len(october.Gaps) != 1 {
		t.Fatalf("gaps = %d, want 1", len(october.Gaps))
	}
	retentionStart := utcHour(2026, 10, 28, 13)
	if !october.Gaps[0].Start.Equal(utcHour(2026, 10, 1, 0)) || !october.Gaps[0].End.Equal(retentionStart) {
		t.Errorf("gap = %s..%s, want 2026-10-01 00:00..%s", october.Gaps[0].Start, october.Gaps[0].End, retentionStart)
	}
	if len(october.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(october.Jobs))
	}
	if !october.Jobs[0].Start.Equal(retentionStart) || !october.Jobs[0].End.Equal(utcHour(2026, 11, 1, 0)) {
		t.Errorf("job = %s..%s, want %s..2026-11-01", october.Jobs[0].Start, october.Jobs[0].End, retentionStart)
	}
}

func TestPlanSkipsFinalizedMonth(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)
	opts := testOptions()
	state := stateWithTrace(t, utcHour(2026, 10, 31, 23))
	state.MarkFinalized("2026-10")

	plan := PlanMonths(now, opts, state, map[string]*persistence.History{})
	for _, month := range plan.Months {
		if month.Month == "2026-10" {
			t.Fatal("finalized October was planned")
		}
	}
	if len(plan.Months) != 1 || plan.Months[0].Month != "2026-11" {
		t.Errorf("plan months = %+v, want only November", plan.Months)
	}
}

func TestPlanInboundJobs(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)
	opts := testOptions()
	opts.CollectInbound = true

	plan := PlanMonths(now, opts, persistence.NewState(), map[string]*persistence.History{})
	jobs := plan.Months[0].Jobs
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 (outbound and inbound)", len(jobs))
	}
	directions := map[persistence.Direction]bool{}
	for _, job := range jobs {
		directions[job.Direction] = true
	}
	if !directions[persistence.DirectionOutbound] || !directions[persistence.DirectionInbound] {
		t.Errorf("directions = %v", directions)
	}
}

func TestPlanNoCompleteHourYet(t *testing.T) {
	now := utcHour(2026, 11, 1, 0).Add(30 * time.Minute)
	plan := PlanMonths(now, testOptions(), persistence.NewState(), map[string]*persistence.History{})
	if len(plan.Months) != 0 {
		t.Errorf("plan = %+v, want no work before the first complete hour", plan.Months)
	}
	if plan.HasWork() {
		t.Error("HasWork() = true")
	}
}

func TestApplyGapsOnlyFillsMissingHours(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a",
		Direction:  persistence.DirectionOutbound,
		Hour:       utcHour(2026, 11, 1, 1),
		Bytes:      123,
		Status:     persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	filled := ApplyGaps(history, []Gap{{
		Month:     "2026-11",
		VNICID:    "vnic-a",
		Direction: persistence.DirectionOutbound,
		Start:     utcHour(2026, 11, 1, 0),
		End:       utcHour(2026, 11, 1, 3),
		Reason:    GapOutOfRetention,
	}}, utcHour(2026, 11, 7, 13))

	if filled != 2 {
		t.Errorf("filled = %d, want 2", filled)
	}
	if history.Len() != 3 {
		t.Errorf("Len() = %d, want 3", history.Len())
	}
	kept, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 1))
	if kept.Bytes != 123 {
		t.Errorf("existing bucket overwritten: %+v", kept)
	}
	for _, hour := range []time.Time{utcHour(2026, 11, 1, 0), utcHour(2026, 11, 1, 2)} {
		bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, hour)
		if bucket.Status != persistence.StatusIncomplete || bucket.Bytes != 0 {
			t.Errorf("gap bucket %s = %+v, want incomplete with 0 bytes", hour, bucket)
		}
	}
}

func TestMarkJobFailedKeepsConfirmedBuckets(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a",
		Direction:  persistence.DirectionOutbound,
		Hour:       utcHour(2026, 11, 1, 0),
		Bytes:      100,
		Status:     persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a",
		Direction:  persistence.DirectionOutbound,
		Hour:       utcHour(2026, 11, 1, 1),
		Bytes:      50,
		Status:     persistence.StatusProvisional,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	job := testJob(utcHour(2026, 11, 1, 0), utcHour(2026, 11, 1, 3))

	failed := MarkJobFailed(history, job, utcHour(2026, 11, 7, 14))
	if failed != 2 {
		t.Errorf("failed = %d, want 2", failed)
	}
	complete, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 0))
	if complete.Status != persistence.StatusComplete || complete.Bytes != 100 {
		t.Errorf("complete bucket changed: %+v", complete)
	}
	provisional, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 1))
	if provisional.Status != persistence.StatusIncomplete || provisional.Bytes != 0 {
		t.Errorf("provisional bucket = %+v, want incomplete with 0 bytes", provisional)
	}
	missing, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 1, 2))
	if missing.Status != persistence.StatusIncomplete {
		t.Errorf("missing bucket = %+v, want incomplete", missing)
	}
}

func TestJobMetadata(t *testing.T) {
	job := Job{
		Month:     "2026-11",
		VNICID:    "ocid1.vnic.oc1.iad.a",
		Direction: persistence.DirectionOutbound,
		Start:     utcHour(2026, 11, 1, 0),
		End:       utcHour(2026, 11, 1, 3),
	}
	if job.Metric() != MetricOutbound {
		t.Errorf("Metric() = %q", job.Metric())
	}
	if job.Hours() != 3 {
		t.Errorf("Hours() = %d, want 3", job.Hours())
	}
	if got := job.MQL(); got != `VnicToNetworkBytes[1h]{resourceId = "ocid1.vnic.oc1.iad.a"}.sum()` {
		t.Errorf("MQL() = %q", got)
	}
	job.Direction = persistence.DirectionInbound
	if job.Metric() != MetricInbound {
		t.Errorf("inbound Metric() = %q", job.Metric())
	}
}

func TestJobQueryEndExtendsOneResolution(t *testing.T) {
	job := Job{End: utcHour(2026, 11, 1, 0)}
	if got := job.QueryEnd(); !got.Equal(utcHour(2026, 11, 1, 1)) {
		t.Errorf("QueryEnd() = %s, want 01:00", got)
	}
}
