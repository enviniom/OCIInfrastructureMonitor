package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

const (
	testVNICA = "ocid1.vnic.oc1.iad.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testVNICB = "ocid1.vnic.oc1.iad.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Network.Enabled = true
	cfg.Network.VNICOCIDs = []string{testVNICA}
	cfg.Network.MonthlyLimitGiB = 100
	cfg.Network.HighTraffic6HGiB = 100
	cfg.Network.HighTrafficCooldownHours = 24
	cfg.Network.NoDataStreakHours = 6
	return cfg
}

func monthNow() time.Time {
	return time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC)
}

func addBucket(t *testing.T, history *persistence.History, id string, hour time.Time, bytes int64, status persistence.Status) {
	t.Helper()
	history.Set(persistence.Bucket{
		ResourceID: id,
		Direction:  persistence.DirectionOutbound,
		Hour:       hour,
		Bytes:      bytes,
		Status:     status,
		CapturedAt: hour.Add(time.Hour),
	})
}

func testInput(now time.Time, histories ...*persistence.History) Input {
	cfg := testConfig()
	byMonth := make(map[string]*persistence.History, len(histories))
	for _, history := range histories {
		byMonth[history.Month] = history
	}
	return Input{
		Now:       now,
		Config:    cfg,
		State:     persistence.NewState(),
		Histories: byMonth,
	}
}

func TestMonthlyThresholdsBasicSeverityAndText(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	// Exactly 50 % of a 100 GiB monthly limit.
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 50*gib, persistence.StatusComplete)

	in := testInput(now, history)
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Kind != persistence.NotificationMonthlyThreshold || alert.Period != "2026-11" {
		t.Errorf("kind/period = %q/%q", alert.Kind, alert.Period)
	}
	if alert.Monthly == nil {
		t.Fatal("Monthly = nil")
	}
	if alert.Monthly.Severity != SeverityInfo {
		t.Errorf("severity = %v, want informativa", alert.Monthly.Severity)
	}
	if alert.Monthly.Threshold != 50 {
		t.Errorf("threshold = %d, want 50", alert.Monthly.Threshold)
	}
	if got := alert.Monthly.Crossed; len(got) != 1 || got[0] != 50 {
		t.Errorf("crossed = %v, want [50]", got)
	}
	if got := alert.Monthly.Percentage; got != "50,0" {
		t.Errorf("percentage = %q, want 50,0", got)
	}
	if !strings.Contains(alert.Text(), "Alerta mensual — informativa") {
		t.Errorf("text = %q", alert.Text())
	}
	if notification := alert.Notification(); notification.Level != 50 {
		t.Errorf("notification level = %d, want 50", notification.Level)
	}
}

func TestSeverityIsAssignedByPosition(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 31*gib, persistence.StatusComplete)

	in := testInput(now, history)
	in.Config.Network.AlertPercentages = []int{10, 20, 30, 100}
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0].Monthly
	if alert == nil {
		t.Fatal("Monthly = nil")
	}
	if alert.Severity != SeverityCritical {
		t.Errorf("severity = %v, want crítica", alert.Severity)
	}
	if alert.Threshold != 30 {
		t.Errorf("threshold = %d, want 30", alert.Threshold)
	}
}

func TestMultipleCrossingsProduceOneAlertWithAllThresholds(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)

	in := testInput(now, history)
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Monthly == nil {
		t.Fatal("Monthly = nil")
	}
	if alert.Monthly.Severity != SeverityCritical {
		t.Errorf("severity = %v, want crítica", alert.Monthly.Severity)
	}
	if alert.Monthly.Threshold != 90 {
		t.Errorf("threshold = %d, want 90", alert.Monthly.Threshold)
	}
	if got := alert.Monthly.Crossed; len(got) != 3 || got[0] != 50 || got[1] != 75 || got[2] != 90 {
		t.Errorf("crossed = %v, want [50 75 90]", got)
	}

	MarkNotified(&in.State, alert, now)
	if got := in.State.Alerts.MonthlyAlerts["2026-11"]; len(got) != 3 || got[0] != 50 || got[1] != 75 || got[2] != 90 {
		t.Fatalf("recorded thresholds = %v, want [50 75 90]", got)
	}

	if again := Evaluate(in, MonthlyThresholds{}); len(again.Alerts) != 0 {
		t.Fatalf("repeated alerts = %d, want 0", len(again.Alerts))
	}
}

func TestThresholdsCrossedBetweenTwoRunsProduceOneAlert(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	hour := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	// First run: below the first threshold, no alert.
	addBucket(t, history, testVNICA, hour, 40*gib, persistence.StatusComplete)
	in := testInput(now, history)
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 0 {
		t.Fatalf("first run alerts = %d, want 0", len(result.Alerts))
	}

	// Second run: 50 %, 75 % and 90 % are crossed at once.
	addBucket(t, history, testVNICA, hour, 95*gib, persistence.StatusComplete)
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("second run alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Monthly == nil {
		t.Fatal("Monthly = nil")
	}
	if alert.Monthly.Severity != SeverityCritical {
		t.Errorf("severity = %v, want crítica", alert.Monthly.Severity)
	}
	if got := alert.Monthly.Crossed; len(got) != 3 || got[0] != 50 || got[1] != 75 || got[2] != 90 {
		t.Errorf("crossed = %v, want [50 75 90]", got)
	}
	MarkNotified(&in.State, alert, now)
	if got := in.State.Alerts.MonthlyAlerts["2026-11"]; len(got) != 3 {
		t.Errorf("recorded thresholds = %v, want three", got)
	}
	if again := Evaluate(in, MonthlyThresholds{}); len(again.Alerts) != 0 {
		t.Fatalf("third run alerts = %d, want 0", len(again.Alerts))
	}
}

func TestLaterThresholdOnlyReportsNewCrossings(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 80*gib, persistence.StatusComplete)

	in := testInput(now, history)
	first := Evaluate(in, MonthlyThresholds{})
	if len(first.Alerts) != 1 {
		t.Fatalf("first alerts = %d, want 1", len(first.Alerts))
	}
	MarkNotified(&in.State, first.Alerts[0], now)

	addBucket(t, history, testVNICA, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), 10*gib, persistence.StatusComplete)
	second := Evaluate(in, MonthlyThresholds{})
	if len(second.Alerts) != 1 {
		t.Fatalf("second alerts = %d, want 1", len(second.Alerts))
	}
	if second.Alerts[0].Monthly == nil {
		t.Fatal("Monthly = nil")
	}
	if got := second.Alerts[0].Monthly.Crossed; len(got) != 1 || got[0] != 90 {
		t.Errorf("crossed = %v, want [90]", got)
	}
	MarkNotified(&in.State, second.Alerts[0], now)
	if got := in.State.Alerts.MonthlyAlerts["2026-11"]; len(got) != 3 {
		t.Errorf("recorded thresholds = %v, want three", got)
	}
}

func TestMonthlyAlertsResetWithTheMonth(t *testing.T) {
	november := persistence.NewHistory("2026-11")
	addBucket(t, november, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)

	in := testInput(time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC), november)
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("november alerts = %d, want 1", len(result.Alerts))
	}
	MarkNotified(&in.State, result.Alerts[0], in.Now)

	december := persistence.NewHistory("2026-12")
	addBucket(t, december, testVNICA, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)
	in.Now = time.Date(2026, 12, 2, 14, 0, 0, 0, time.UTC)
	in.Histories["2026-12"] = december

	next := Evaluate(in, MonthlyThresholds{})
	if len(next.Alerts) != 1 {
		t.Fatalf("december alerts = %d, want 1", len(next.Alerts))
	}
	if next.Alerts[0].Period != "2026-12" {
		t.Errorf("december period = %q", next.Alerts[0].Period)
	}
	if got := next.Alerts[0].Monthly.Crossed; len(got) != 3 {
		t.Errorf("december crossed = %v, want [50 75 90]", got)
	}
}

func TestFinalizedAndPreviousMonthsAreNotEvaluated(t *testing.T) {
	now := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)

	finalized := persistence.NewHistory("2026-11")
	addBucket(t, finalized, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)
	finalized.Finalize(now)

	october := persistence.NewHistory("2026-10")
	addBucket(t, october, testVNICA, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)

	november := persistence.NewHistory("2026-11")
	addBucket(t, november, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 1*gib, persistence.StatusComplete)

	in := testInput(now, finalized, october, november)
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts = %d, want 0 for finalized/current-below and previous month", len(result.Alerts))
	}
}

func TestIncompleteAccumulationIsStatedExplicitly(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusIncomplete)

	result := Evaluate(testInput(now, history), MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Monthly == nil || len(alert.Monthly.Warnings) == 0 {
		t.Fatalf("warnings = %+v, want at least one", alert.Monthly)
	}
	if !strings.Contains(alert.Text(), "acumulado es incompleto") {
		t.Errorf("text does not state the incomplete total: %q", alert.Text())
	}
}

func TestProvisionalAccumulationIsStatedExplicitly(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 6, 23, 0, 0, 0, time.UTC), 95*gib, persistence.StatusProvisional)

	result := Evaluate(testInput(now, history), MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Monthly == nil || len(alert.Monthly.Warnings) == 0 {
		t.Fatalf("warnings = %+v, want at least one", alert.Monthly)
	}
	if !strings.Contains(alert.Text(), "acumulado es provisional") {
		t.Errorf("text does not state the provisional total: %q", alert.Text())
	}
}

func TestPercentageIsTruncatedAndExactBytesAreCompared(t *testing.T) {
	now := monthNow()
	limit := int64(100 * gib)
	ninety := percentageBytes(limit, 90)

	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), ninety-1, persistence.StatusComplete)

	result := Evaluate(testInput(now, history), MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0].Monthly
	if alert == nil {
		t.Fatal("Monthly = nil")
	}
	if alert.Percentage != "89,9" {
		t.Errorf("percentage = %q, want 89,9", alert.Percentage)
	}
	if alert.Threshold != 75 {
		t.Errorf("threshold = %d, want 75 (90 %% must not fire at 89,9 %%)", alert.Threshold)
	}

	// One byte more reaches the 90 % threshold exactly.
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), ninety, persistence.StatusComplete)
	exact := Evaluate(testInput(now, history), MonthlyThresholds{})
	if len(exact.Alerts) != 1 {
		t.Fatalf("exact alerts = %d, want 1", len(exact.Alerts))
	}
	if exact.Alerts[0].Monthly == nil || exact.Alerts[0].Monthly.Threshold != 90 {
		t.Errorf("exact threshold = %+v, want 90", exact.Alerts[0].Monthly)
	}
	if got := exact.Alerts[0].Monthly.Percentage; got != "90,0" {
		t.Errorf("exact percentage = %q, want 90,0", got)
	}
}

func TestNoAlertWithoutHistoryOrThreshold(t *testing.T) {
	in := testInput(monthNow())
	in.Histories = nil
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts without history = %d, want 0", len(result.Alerts))
	}

	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 0, persistence.StatusNoData)
	in = testInput(monthNow(), history)
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts with no_data = %d, want 0", len(result.Alerts))
	}
}

func TestPendingMonthlyAlertSuppressesDuplicateProposal(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)

	in := testInput(now, history)
	in.State.Pending = []persistence.Notification{{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     90,
		CreatedAt: now.Add(-time.Hour),
	}}
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 0 {
		t.Fatalf("alerts with pending equal level = %d, want 0", len(result.Alerts))
	}

	in.State.Pending[0].Level = 75
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 1 {
		t.Fatalf("alerts with lower pending level = %d, want 1", len(result.Alerts))
	}

	in.State.Pending[0].Period = "2026-10"
	in.State.Pending[0].Level = 90
	if result := Evaluate(in, MonthlyThresholds{}); len(result.Alerts) != 1 {
		t.Fatalf("alerts with pending other period = %d, want 1", len(result.Alerts))
	}
}

func TestNeverVisibleVNICKeepsTheMonthlyTotalIncomplete(t *testing.T) {
	now := monthNow()
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 95*gib, persistence.StatusComplete)
	// testVNICB never produced a datapoint: every one of its hours is
	// incomplete, never no_data.
	addBucket(t, history, testVNICB, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 0, persistence.StatusIncomplete)

	in := testInput(now, history)
	in.Config.Network.VNICOCIDs = []string{testVNICA, testVNICB}
	result := Evaluate(in, MonthlyThresholds{})
	if len(result.Alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(result.Alerts))
	}
	alert := result.Alerts[0]
	if alert.Monthly == nil || alert.Monthly.AccumulatedStatus != persistence.StatusIncomplete {
		t.Fatalf("accumulated status = %+v, want incomplete", alert.Monthly)
	}
	if !strings.Contains(alert.Text(), "acumulado es incompleto") {
		t.Errorf("text = %q", alert.Text())
	}
}
