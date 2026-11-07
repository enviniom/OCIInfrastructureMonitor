package alerts

import (
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func TestMarkNotifiedMergesThresholds(t *testing.T) {
	state := persistence.NewState()
	at := monthNow()

	MarkNotified(&state, Alert{
		Kind:   persistence.NotificationMonthlyThreshold,
		Period: "2026-11",
		Monthly: &MonthlyAlert{
			Month:   "2026-11",
			Crossed: []int{75},
		},
	}, at)
	MarkNotified(&state, Alert{
		Kind:   persistence.NotificationMonthlyThreshold,
		Period: "2026-11",
		Monthly: &MonthlyAlert{
			Month:   "2026-11",
			Crossed: []int{90, 50},
		},
	}, at)

	got := state.Alerts.MonthlyAlerts["2026-11"]
	if len(got) != 3 || got[0] != 50 || got[1] != 75 || got[2] != 90 {
		t.Fatalf("crossed = %v, want [50 75 90]", got)
	}

	// Repeating the same delivery is idempotent.
	MarkNotified(&state, Alert{
		Kind:   persistence.NotificationMonthlyThreshold,
		Period: "2026-11",
		Monthly: &MonthlyAlert{
			Month:   "2026-11",
			Crossed: []int{50, 75, 90},
		},
	}, at)
	if got := state.Alerts.MonthlyAlerts["2026-11"]; len(got) != 3 {
		t.Errorf("crossed after repeat = %v, want three", got)
	}
}

func TestMarkNotifiedAnomalyStartsCooldown(t *testing.T) {
	state := persistence.NewState()
	at := monthNow()
	if state.Alerts.Anomaly6H != nil {
		t.Fatal("fresh state has anomaly bookkeeping")
	}
	MarkNotified(&state, Alert{Kind: persistence.NotificationAnomaly6H}, at)
	if state.Alerts.Anomaly6H == nil || state.Alerts.Anomaly6H.LastNotifiedAt == nil {
		t.Fatal("anomaly bookkeeping not recorded")
	}
	if !state.Alerts.Anomaly6H.LastNotifiedAt.Equal(at) {
		t.Errorf("last notified = %s, want %s", state.Alerts.Anomaly6H.LastNotifiedAt, at)
	}
}

func TestMarkNotifiedIgnoresIncompleteAlerts(t *testing.T) {
	state := persistence.NewState()
	MarkNotified(&state, Alert{Kind: persistence.NotificationMonthlyThreshold}, monthNow())
	MarkNotified(&state, Alert{Kind: "custom"}, monthNow())
	if len(state.Alerts.MonthlyAlerts) != 0 || state.Alerts.Anomaly6H != nil {
		t.Errorf("unexpected state mutation: %+v", state.Alerts)
	}
}

func TestNotificationMapping(t *testing.T) {
	at := monthNow()
	monthly := Alert{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     0,
		CreatedAt: at,
		Monthly:   &MonthlyAlert{Month: "2026-11", Severity: SeverityCritical, Threshold: 90},
	}
	notification := monthly.Notification()
	if notification.Kind != persistence.NotificationMonthlyThreshold ||
		notification.Period != "2026-11" || notification.Level != 90 ||
		!notification.CreatedAt.Equal(at) {
		t.Errorf("notification = %+v", notification)
	}

	anomaly := Alert{Kind: persistence.NotificationAnomaly6H, Period: "2026-11", CreatedAt: at, Anomaly: &AnomalyAlert{}}
	notification = anomaly.Notification()
	if notification.Level != 0 || notification.Period != "2026-11" {
		t.Errorf("anomaly notification = %+v", notification)
	}
}

type fixedEvaluator struct{ alerts []Alert }

func (e fixedEvaluator) Name() string { return "fixed" }

func (e fixedEvaluator) Evaluate(Input) []Alert { return e.alerts }

func TestEvaluateIsExtensible(t *testing.T) {
	custom := fixedEvaluator{alerts: []Alert{{Kind: "custom"}}}
	result := Evaluate(Input{Now: monthNow()}, custom)
	if len(result.Alerts) != 1 || result.Alerts[0].Kind != "custom" {
		t.Fatalf("explicit evaluator result = %+v", result.Alerts)
	}

	defaults := Evaluate(Input{Now: monthNow()})
	if len(defaults.Alerts) != 0 {
		t.Errorf("defaults with empty input = %d alerts, want 0", len(defaults.Alerts))
	}
}

func TestAlertTextWithoutPayloadIsEmpty(t *testing.T) {
	if got := (Alert{}).Text(); got != "" {
		t.Errorf("empty alert text = %q", got)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	history := persistence.NewHistory("2026-11")
	addBucket(t, history, testVNICA, time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), 20*gib, persistence.StatusComplete)
	in := testInput(time.Date(2026, 11, 7, 14, 30, 0, 0, time.UTC), history)
	first := Evaluate(in)
	second := Evaluate(in)
	if len(first.Alerts) != len(second.Alerts) {
		t.Fatalf("runs differ: %d vs %d", len(first.Alerts), len(second.Alerts))
	}
	for i := range first.Alerts {
		if first.Alerts[i].Kind != second.Alerts[i].Kind || first.Alerts[i].Period != second.Alerts[i].Period {
			t.Errorf("alert %d differs between runs", i)
		}
	}
}

func TestMarkNotificationNotifiedRecordsCollapsedThresholds(t *testing.T) {
	state := persistence.NewState()
	at := monthNow()
	MarkNotificationNotified(&state, persistence.Notification{
		Kind:   persistence.NotificationMonthlyThreshold,
		Period: "2026-11",
		Level:  90,
	}, []int{50, 75, 90, 100}, at)

	got := state.Alerts.MonthlyAlerts["2026-11"]
	if len(got) != 3 || got[0] != 50 || got[1] != 75 || got[2] != 90 {
		t.Fatalf("crossed = %v, want [50 75 90]", got)
	}

	MarkNotificationNotified(&state, persistence.Notification{
		Kind:   persistence.NotificationAnomaly6H,
		Period: "2026-11",
	}, []int{50, 75, 90, 100}, at)
	if state.Alerts.Anomaly6H == nil || state.Alerts.Anomaly6H.LastNotifiedAt == nil {
		t.Fatal("anomaly delivery not recorded")
	}
}
