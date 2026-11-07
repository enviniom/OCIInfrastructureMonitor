package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func fillHours(t *testing.T, history *persistence.History, id string, start time.Time, count int, bytes int64, status persistence.Status) {
	t.Helper()
	for i := 0; i < count; i++ {
		addBucket(t, history, id, start.Add(time.Duration(i)*time.Hour), bytes, status)
	}
}

func TestAnomalyWindowUsesTheLastSixCompleteHours(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	stats := windowStats(now, []string{testVNICA}, nil)
	wantStart := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	if !stats.Start.Equal(wantStart) || !stats.End.Equal(wantEnd) {
		t.Errorf("window = [%s, %s), want [%s, %s)", stats.Start, stats.End, wantStart, wantEnd)
	}
	if stats.Hours() != 6 {
		t.Errorf("hours = %d, want 6", stats.Hours())
	}
	if stats.Missing != 6 {
		t.Errorf("missing = %d, want 6 without histories", stats.Missing)
	}

	// An instant exactly on the hour leaves the hour that starts at now out.
	aligned := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	stats = windowStats(aligned, []string{testVNICA}, nil)
	if !stats.End.Equal(aligned) || !stats.Start.Equal(time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("aligned window = [%s, %s)", stats.Start, stats.End)
	}
}

func TestAnomalyFiresAboveThresholdOverSixCompleteHours(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	fillHours(t, history, testVNICA, time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), 6, 20*gib, persistence.StatusComplete)

	result := Evaluate(testInput(now, history), Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Kind != persistence.NotificationAnomaly6H || alert.Period != "2026-11" {
		t.Errorf("kind/period = %q/%q", alert.Kind, alert.Period)
	}
	if alert.Anomaly == nil {
		t.Fatal("Anomaly = nil")
	}
	if alert.Anomaly.Window.Bytes != 120*gib {
		t.Errorf("bytes = %d, want %d", alert.Anomaly.Window.Bytes, 120*gib)
	}
	if alert.Anomaly.ThresholdBytes != 100*gib {
		t.Errorf("threshold = %d, want %d", alert.Anomaly.ThresholdBytes, 100*gib)
	}
	if !alert.Anomaly.Reliable || len(alert.Anomaly.Warnings) != 0 {
		t.Errorf("reliable/warnings = %v/%v, want true/[]", alert.Anomaly.Reliable, alert.Anomaly.Warnings)
	}
	if notification := alert.Notification(); notification.Level != 0 || notification.Period != "2026-11" {
		t.Errorf("notification = %+v", notification)
	}
	text := alert.Text()
	if !strings.Contains(text, "consumo anormal") || strings.Contains(text, "AVISO") {
		t.Errorf("text = %q", text)
	}
}

func TestAnomalyComparesExactBytes(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

	below := persistence.NewHistory("2026-11")
	fillHours(t, below, testVNICA, start, 5, 20*gib, persistence.StatusComplete)
	fillHours(t, below, testVNICA, start.Add(5*time.Hour), 1, 0, persistence.StatusComplete)
	if result := Evaluate(testInput(now, below), Anomaly6H{}); len(result.Alerts) != 1 {
		t.Fatalf("exact threshold alerts = %d, want 1", len(result.Alerts))
	}

	under := persistence.NewHistory("2026-11")
	fillHours(t, under, testVNICA, start, 5, 19*gib, persistence.StatusComplete)
	fillHours(t, under, testVNICA, start.Add(5*time.Hour), 1, 0, persistence.StatusComplete)
	if result := Evaluate(testInput(now, under), Anomaly6H{}); len(result.Alerts) != 0 {
		t.Fatalf("under threshold alerts = %d, want 0", len(result.Alerts))
	}
}

func TestAnomalyWarnsAboutProvisionalAndIncompleteBuckets(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

	provisional := persistence.NewHistory("2026-11")
	fillHours(t, provisional, testVNICA, start, 5, 24*gib, persistence.StatusComplete)
	addBucket(t, provisional, testVNICA, start.Add(5*time.Hour), 0, persistence.StatusProvisional)
	result := Evaluate(testInput(now, provisional), Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("provisional alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Anomaly == nil || alert.Anomaly.Reliable || len(alert.Anomaly.Warnings) == 0 {
		t.Fatalf("provisional anomaly = %+v, want unreliable with warnings", alert.Anomaly)
	}
	if !strings.Contains(alert.Text(), "AVISO") || !strings.Contains(alert.Text(), "provisional") {
		t.Errorf("text = %q", alert.Text())
	}

	incomplete := persistence.NewHistory("2026-11")
	fillHours(t, incomplete, testVNICA, start, 5, 24*gib, persistence.StatusComplete)
	addBucket(t, incomplete, testVNICA, start.Add(5*time.Hour), 0, persistence.StatusIncomplete)
	result = Evaluate(testInput(now, incomplete), Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("incomplete alerts = %d, want 1", len(result.Alerts))
	}
	alert = result.Alerts[0]
	if alert.Anomaly == nil || alert.Anomaly.Reliable {
		t.Fatalf("incomplete anomaly = %+v, want unreliable", alert.Anomaly)
	}
	if !strings.Contains(alert.Text(), "incompleto") {
		t.Errorf("text = %q", alert.Text())
	}
}

func TestAnomalyWarnsAboutMissingBuckets(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	// 5 buckets of 24 GiB plus one missing: 120 GiB over a window of 6.
	fillHours(t, history, testVNICA, start, 5, 24*gib, persistence.StatusComplete)

	result := Evaluate(testInput(now, history), Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Anomaly == nil || alert.Anomaly.Window.Missing != 1 {
		t.Fatalf("missing = %+v, want 1", alert.Anomaly)
	}
	if alert.Anomaly.Reliable || !strings.Contains(alert.Text(), "falta 1 bucket") {
		t.Errorf("reliable/text = %v/%q", alert.Anomaly.Reliable, alert.Text())
	}
}

func TestAnomalyNoDataStreakOnlyWarnsWhenZerosArePublished(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	// Three idle hours followed by three 40 GiB hours: the total still
	// exceeds the threshold and an isolated no_data never invalidates it.
	fillHours(t, history, testVNICA, start, 3, 0, persistence.StatusNoData)
	fillHours(t, history, testVNICA, start.Add(3*time.Hour), 3, 40*gib, persistence.StatusComplete)

	in := testInput(now, history)
	in.Config.Network.NoDataStreakHours = 2
	result := Evaluate(in, Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts with omitted zeros = %d, want 1", len(result.Alerts))
	}
	if alert := result.Alerts[0].Anomaly; alert == nil || !alert.Reliable || alert.Window.MaxNoDataStreak != 3 {
		t.Fatalf("anomaly = %+v, want reliable with a 3 h streak", alert)
	}

	stats := result.Alerts[0].Anomaly.Window
	warnings := anomalyWarnings(stats, accumulate.Options{PublishZeros: true, NoDataStreak: 2 * time.Hour})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no_data") {
		t.Errorf("warnings with published zeros = %v, want one no_data warning", warnings)
	}
	if got := anomalyWarnings(stats, accumulate.Options{PublishZeros: false, NoDataStreak: 2 * time.Hour}); len(got) != 0 {
		t.Errorf("warnings with omitted zeros = %v, want none", got)
	}
}

func TestAnomalyCooldown(t *testing.T) {
	first := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	fillHours(t, history, testVNICA, time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), 6, 20*gib, persistence.StatusComplete)
	// Cover the two later windows too (hours 07 to 13 of November 8).
	fillHours(t, history, testVNICA, time.Date(2026, 11, 8, 7, 0, 0, 0, time.UTC), 7, 20*gib, persistence.StatusComplete)

	in := testInput(first, history)
	result := Evaluate(in, Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("first alerts = %d, want 1", len(result.Alerts))
	}
	MarkNotified(&in.State, result.Alerts[0], first)

	in.Now = time.Date(2026, 11, 8, 13, 30, 0, 0, time.UTC)
	if result := Evaluate(in, Anomaly6H{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts inside cooldown = %d, want 0", len(result.Alerts))
	}

	in.Now = time.Date(2026, 11, 8, 14, 30, 0, 0, time.UTC)
	if result := Evaluate(in, Anomaly6H{}); len(result.Alerts) != 1 {
		t.Fatalf("alerts after cooldown = %d, want 1", len(result.Alerts))
	}
}

func TestAnomalyWindowCrossesTheMonthBoundary(t *testing.T) {
	now := time.Date(2026, 11, 1, 2, 30, 0, 0, time.UTC)
	october := persistence.NewHistory("2026-10")
	november := persistence.NewHistory("2026-11")
	fillHours(t, october, testVNICA, time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC), 4, 20*gib, persistence.StatusComplete)
	fillHours(t, november, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 2, 20*gib, persistence.StatusComplete)

	result := Evaluate(testInput(now, october, november), Anomaly6H{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Anomaly == nil {
		t.Fatal("Anomaly = nil")
	}
	wantStart := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	if !alert.Anomaly.Window.Start.Equal(wantStart) || alert.Anomaly.Window.Missing != 0 {
		t.Errorf("window = %+v, want start %s and no missing buckets", alert.Anomaly.Window, wantStart)
	}
	if alert.Period != "2026-11" {
		t.Errorf("period = %q, want 2026-11", alert.Period)
	}
}

func TestAnomalyIgnoresInboundTraffic(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	start := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	fillHours(t, history, testVNICA, start, 6, 10*gib, persistence.StatusComplete)
	for i := 0; i < 6; i++ {
		history.Set(persistence.Bucket{
			ResourceID: testVNICA,
			Direction:  persistence.DirectionInbound,
			Hour:       start.Add(time.Duration(i) * time.Hour),
			Bytes:      1000 * gib,
			Status:     persistence.StatusComplete,
			CapturedAt: start.Add(time.Duration(i+1) * time.Hour),
		})
	}

	if result := Evaluate(testInput(now, history), Anomaly6H{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts = %d, want 0: inbound must never be added", len(result.Alerts))
	}
}

func TestPendingAnomalyAlertSuppressesDuplicateProposal(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
	history := persistence.NewHistory("2026-11")
	fillHours(t, history, testVNICA, time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), 6, 20*gib, persistence.StatusComplete)

	in := testInput(now, history)
	in.State.Pending = []persistence.Notification{{
		Kind:      persistence.NotificationAnomaly6H,
		Period:    "2026-11",
		CreatedAt: now.Add(-time.Hour),
	}}
	if result := Evaluate(in, Anomaly6H{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts with pending anomaly = %d, want 0", len(result.Alerts))
	}
}
