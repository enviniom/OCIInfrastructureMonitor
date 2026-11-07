package accumulate

import (
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func testJob(start, end time.Time) Job {
	return Job{
		Month:     persistence.FormatMonth(start),
		VNICID:    "vnic-a",
		Direction: persistence.DirectionOutbound,
		Start:     start,
		End:       end,
	}
}

func sampleAt(hour time.Time, bytes int64) Sample {
	return Sample{ResourceID: "vnic-a", Direction: persistence.DirectionOutbound, Hour: hour, Bytes: bytes}
}

func TestReconcileClassifiesHours(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 12))
	firstVisible := utcHour(2026, 11, 7, 8)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible, Visible: true}

	history := persistence.NewHistory("2026-11")
	samples := []Sample{
		sampleAt(utcHour(2026, 11, 7, 8), 100),
		sampleAt(utcHour(2026, 11, 7, 9), 200),
		sampleAt(utcHour(2026, 11, 7, 11), 400),
	}

	result := Reconcile(history, job, samples, now, testOptions(), vnic)
	if result.NewBuckets != 4 {
		t.Errorf("NewBuckets = %d, want 4", result.NewBuckets)
	}
	if result.Corrections.Buckets != 0 {
		t.Errorf("Corrections = %+v, want none", result.Corrections)
	}

	want := map[time.Time]struct {
		bytes  int64
		status persistence.Status
	}{
		utcHour(2026, 11, 7, 8):  {100, persistence.StatusComplete},
		utcHour(2026, 11, 7, 9):  {200, persistence.StatusComplete},
		utcHour(2026, 11, 7, 10): {0, persistence.StatusNoData},
		utcHour(2026, 11, 7, 11): {400, persistence.StatusProvisional},
	}
	for hour, expected := range want {
		bucket, ok := history.Get("vnic-a", persistence.DirectionOutbound, hour)
		if !ok {
			t.Errorf("bucket %s missing", hour)
			continue
		}
		if bucket.Bytes != expected.bytes || bucket.Status != expected.status {
			t.Errorf("bucket %s = (%d, %s), want (%d, %s)", hour, bucket.Bytes, bucket.Status, expected.bytes, expected.status)
		}
		if !bucket.CapturedAt.Equal(now) {
			t.Errorf("bucket %s captured_at = %s, want %s", hour, bucket.CapturedAt, now)
		}
	}
}

func TestReconcileBeforeFirstVisibleIsIncomplete(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 12))
	firstVisible := utcHour(2026, 11, 7, 9)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible, Visible: true}

	history := persistence.NewHistory("2026-11")
	Reconcile(history, job, []Sample{sampleAt(utcHour(2026, 11, 7, 9), 200)}, now, testOptions(), vnic)

	bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 8))
	if bucket.Status != persistence.StatusIncomplete {
		t.Errorf("hour before first visible = %s, want incomplete", bucket.Status)
	}
	bucket, _ = history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 9))
	if bucket.Status != persistence.StatusComplete || bucket.Bytes != 200 {
		t.Errorf("visible hour = (%d, %s), want (200, complete)", bucket.Bytes, bucket.Status)
	}
}

func TestReconcileNeverVisibleIsIncomplete(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 11))
	history := persistence.NewHistory("2026-11")

	Reconcile(history, job, nil, now, testOptions(), persistence.VNICState{})

	for hour := utcHour(2026, 11, 7, 8); hour.Before(utcHour(2026, 11, 7, 11)); hour = hour.Add(time.Hour) {
		bucket, ok := history.Get("vnic-a", persistence.DirectionOutbound, hour)
		if !ok {
			t.Fatalf("bucket %s missing", hour)
		}
		if bucket.Status != persistence.StatusIncomplete {
			t.Errorf("hour %s = %s, want incomplete", hour, bucket.Status)
		}
		if bucket.Bytes != 0 {
			t.Errorf("hour %s bytes = %d, want 0", hour, bucket.Bytes)
		}
	}
}

func TestReconcileRecentMissingHourIsProvisional(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 12), utcHour(2026, 11, 7, 13))
	firstVisible := utcHour(2026, 11, 1, 0)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible}

	history := persistence.NewHistory("2026-11")
	Reconcile(history, job, nil, now, testOptions(), vnic)

	bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 12))
	if bucket.Status != persistence.StatusProvisional {
		t.Errorf("recent missing hour = %s, want provisional", bucket.Status)
	}
}

func TestReconcileOverwriteByKeyDoesNotDuplicate(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 12))
	firstVisible := utcHour(2026, 11, 7, 8)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible}
	samples := []Sample{
		sampleAt(utcHour(2026, 11, 7, 8), 100),
		sampleAt(utcHour(2026, 11, 7, 9), 200),
		sampleAt(utcHour(2026, 11, 7, 11), 400),
	}

	history := persistence.NewHistory("2026-11")
	Reconcile(history, job, samples, now, testOptions(), vnic)
	before := history.Len()

	second := Reconcile(history, job, samples, now, testOptions(), vnic)
	if second.NewBuckets != 0 {
		t.Errorf("second NewBuckets = %d, want 0", second.NewBuckets)
	}
	if second.Corrections.Buckets != 0 {
		t.Errorf("second Corrections = %+v, want none", second.Corrections)
	}
	if history.Len() != before {
		t.Errorf("Len() = %d, want %d (overwrite by key must not duplicate)", history.Len(), before)
	}
}

func TestReconcileCountsCorrections(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 12))
	firstVisible := utcHour(2026, 11, 7, 8)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible}
	history := persistence.NewHistory("2026-11")

	Reconcile(history, job, []Sample{
		sampleAt(utcHour(2026, 11, 7, 8), 100),
		sampleAt(utcHour(2026, 11, 7, 9), 200),
		sampleAt(utcHour(2026, 11, 7, 11), 400),
	}, now, testOptions(), vnic)

	result := Reconcile(history, job, []Sample{
		sampleAt(utcHour(2026, 11, 7, 8), 150),
		sampleAt(utcHour(2026, 11, 7, 9), 200),
		sampleAt(utcHour(2026, 11, 7, 11), 400),
	}, now, testOptions(), vnic)

	if result.Corrections.Buckets != 1 {
		t.Errorf("Corrections.Buckets = %d, want 1", result.Corrections.Buckets)
	}
	if result.Corrections.DeltaBytes != 50 {
		t.Errorf("Corrections.DeltaBytes = %d, want 50", result.Corrections.DeltaBytes)
	}
	bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 8))
	if bucket.Bytes != 150 {
		t.Errorf("corrected bytes = %d, want 150", bucket.Bytes)
	}
}

func TestReconcileFinalizedDiscardsLateCorrection(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 12))
	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a",
		Direction:  persistence.DirectionOutbound,
		Hour:       utcHour(2026, 11, 7, 8),
		Bytes:      100,
		Status:     persistence.StatusComplete,
		CapturedAt: now,
	})
	history.Finalize(now)

	result := Reconcile(history, job, []Sample{
		sampleAt(utcHour(2026, 11, 7, 8), 150),
		sampleAt(utcHour(2026, 11, 7, 9), 50),
	}, now, testOptions(), persistence.VNICState{})

	if result.LateCorrections.Buckets != 2 {
		t.Errorf("LateCorrections.Buckets = %d, want 2", result.LateCorrections.Buckets)
	}
	if result.LateCorrections.DeltaBytes != 100 {
		t.Errorf("LateCorrections.DeltaBytes = %d, want 100", result.LateCorrections.DeltaBytes)
	}
	if history.Len() != 1 {
		t.Errorf("finalized history changed: Len() = %d, want 1", history.Len())
	}
	bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 8))
	if bucket.Bytes != 100 {
		t.Errorf("finalized bucket bytes = %d, want 100", bucket.Bytes)
	}
}

func TestReconcileIgnoresSamplesOutsideWindow(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 10), utcHour(2026, 11, 7, 12))
	history := persistence.NewHistory("2026-11")

	result := Reconcile(history, job, []Sample{sampleAt(utcHour(2026, 11, 7, 8), 999)}, now, testOptions(), persistence.VNICState{})
	if result.Samples != 0 {
		t.Errorf("Samples = %d, want 0", result.Samples)
	}
	if result.NewBuckets != 2 {
		t.Errorf("NewBuckets = %d, want 2", result.NewBuckets)
	}
	if _, ok := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 8)); ok {
		t.Error("bucket outside the window was written")
	}
}

func TestReconcileOmittedPointKeepsConfirmedData(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 8), utcHour(2026, 11, 7, 10))
	firstVisible := utcHour(2026, 11, 1, 0)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible}

	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a", Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 7, 8), Bytes: 100, Status: persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 7, 12),
	})
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a", Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 7, 9), Bytes: 400, Status: persistence.StatusProvisional,
		CapturedAt: utcHour(2026, 11, 7, 12),
	})

	result := Reconcile(history, job, nil, now, testOptions(), vnic)
	if result.NewBuckets != 0 || result.Corrections.Buckets != 0 {
		t.Errorf("result = %+v, want no changes", result)
	}
	confirmed, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 8))
	if confirmed.Bytes != 100 || confirmed.Status != persistence.StatusComplete {
		t.Errorf("confirmed bucket = %+v, want (100, complete)", confirmed)
	}
	observed, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 9))
	if observed.Bytes != 400 || observed.Status != persistence.StatusComplete {
		t.Errorf("observed bucket = %+v, want (400, complete)", observed)
	}
}

func TestReconcileProvisionalWithoutDataBecomesNoData(t *testing.T) {
	now := utcHour(2026, 11, 7, 13).Add(30 * time.Minute)
	job := testJob(utcHour(2026, 11, 7, 9), utcHour(2026, 11, 7, 10))
	firstVisible := utcHour(2026, 11, 1, 0)
	vnic := persistence.VNICState{FirstVisible: &firstVisible, LastVisible: &firstVisible}

	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: "vnic-a", Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 7, 9), Bytes: 0, Status: persistence.StatusProvisional,
		CapturedAt: utcHour(2026, 11, 7, 12),
	})

	Reconcile(history, job, nil, now, testOptions(), vnic)
	bucket, _ := history.Get("vnic-a", persistence.DirectionOutbound, utcHour(2026, 11, 7, 9))
	if bucket.Status != persistence.StatusNoData {
		t.Errorf("bucket = %s, want no_data", bucket.Status)
	}
}

func TestUpdateVNIC(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)

	t.Run("never visible does not warn", func(t *testing.T) {
		updated, warned := UpdateVNIC(persistence.VNICState{}, nil, now)
		if warned {
			t.Error("warned = true, want false")
		}
		if updated.Visible || updated.WarningSince != nil {
			t.Errorf("updated = %+v", updated)
		}
	})

	t.Run("visible before and absent now warns once", func(t *testing.T) {
		first := utcHour(2026, 11, 1, 0)
		last := utcHour(2026, 11, 6, 12)
		previous := persistence.VNICState{FirstVisible: &first, LastVisible: &last, Visible: true}
		updated, warned := UpdateVNIC(previous, nil, now)
		if !warned {
			t.Fatal("warned = false, want true")
		}
		if updated.Visible {
			t.Error("Visible = true, want false")
		}
		if updated.WarningSince == nil || !updated.WarningSince.Equal(now) {
			t.Errorf("WarningSince = %v, want %s", updated.WarningSince, now)
		}
		again, warnedAgain := UpdateVNIC(updated, nil, now.Add(time.Hour))
		if warnedAgain {
			t.Error("second warning = true, want false")
		}
		if again.WarningSince == nil || !again.WarningSince.Equal(now) {
			t.Errorf("WarningSince changed to %v", again.WarningSince)
		}
	})

	t.Run("data clears the warning", func(t *testing.T) {
		first := utcHour(2026, 11, 1, 0)
		last := utcHour(2026, 11, 5, 12)
		warning := utcHour(2026, 11, 6, 0)
		previous := persistence.VNICState{FirstVisible: &first, LastVisible: &last, Visible: false, WarningSince: &warning}
		updated, warned := UpdateVNIC(previous, []time.Time{utcHour(2026, 11, 7, 12)}, now)
		if warned {
			t.Error("warned = true, want false")
		}
		if !updated.Visible || updated.WarningSince != nil {
			t.Errorf("updated = %+v", updated)
		}
		if !updated.FirstVisible.Equal(first) {
			t.Errorf("FirstVisible = %s, want %s", updated.FirstVisible, first)
		}
		if !updated.LastVisible.Equal(utcHour(2026, 11, 7, 12)) {
			t.Errorf("LastVisible = %s, want 2026-11-07 12:00", updated.LastVisible)
		}
		if updated.LastDataAt == nil || !updated.LastDataAt.Equal(now) {
			t.Errorf("LastDataAt = %v, want %s", updated.LastDataAt, now)
		}
	})

	t.Run("first visible takes the oldest hour", func(t *testing.T) {
		updated, _ := UpdateVNIC(persistence.VNICState{}, []time.Time{
			utcHour(2026, 11, 7, 12),
			utcHour(2026, 11, 7, 10),
		}, now)
		if !updated.FirstVisible.Equal(utcHour(2026, 11, 7, 10)) {
			t.Errorf("FirstVisible = %s, want 10:00", updated.FirstVisible)
		}
		if !updated.LastVisible.Equal(utcHour(2026, 11, 7, 12)) {
			t.Errorf("LastVisible = %s, want 12:00", updated.LastVisible)
		}
	})
}
