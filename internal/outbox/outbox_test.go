package outbox

import (
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func utc(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func notification(kind, period string, level int, at time.Time) persistence.Notification {
	return persistence.Notification{Kind: kind, Period: period, Level: level, CreatedAt: at, Text: "texto"}
}

func TestExpireDropsOldEntries(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	old := notification(persistence.NotificationAnomaly6H, "2026-10", 0, now.Add(-TTL-time.Minute))
	exact := notification(persistence.NotificationAnomaly6H, "2026-10", 0, now.Add(-TTL))
	fresh := notification(persistence.NotificationAnomaly6H, "2026-11", 0, now.Add(-time.Hour))

	kept, expired := Expire([]persistence.Notification{old, exact, fresh}, now)
	if len(expired) != 2 {
		t.Fatalf("expired = %d, want 2", len(expired))
	}
	if len(kept) != 1 || !Same(kept[0], fresh) {
		t.Fatalf("kept = %+v", kept)
	}
}

func TestEnqueueAddsAndSorts(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	later := notification(persistence.NotificationAnomaly6H, "2026-11", 0, now.Add(time.Hour))
	earlier := notification(persistence.NotificationMonthlyThreshold, "2026-11", 50, now)

	pending, result := Enqueue(nil, later, now)
	if !result.Added || result.Collapsed {
		t.Fatalf("result = %+v", result)
	}
	pending, result = Enqueue(pending, earlier, now)
	if !result.Added {
		t.Fatalf("result = %+v", result)
	}
	if len(pending) != 2 || !Same(pending[0], earlier) || !Same(pending[1], later) {
		t.Fatalf("pending = %+v, want sorted by created_at", pending)
	}
}

func TestEnqueueCollapsesMonthlyToHighestLevel(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	initial := notification(persistence.NotificationMonthlyThreshold, "2026-11", 50, now)
	initial.Attempts = 2
	pending, _ := Enqueue(nil, initial, now)

	higher := notification(persistence.NotificationMonthlyThreshold, "2026-11", 90, now.Add(time.Minute))
	pending, result := Enqueue(pending, higher, now)
	if !result.Added || !result.Collapsed {
		t.Fatalf("result = %+v", result)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want one entry", pending)
	}
	if pending[0].Level != 90 {
		t.Errorf("level = %d, want 90", pending[0].Level)
	}
	if pending[0].Attempts != 2 {
		t.Errorf("attempts = %d, want the previous attempts kept", pending[0].Attempts)
	}

	lower := notification(persistence.NotificationMonthlyThreshold, "2026-11", 75, now.Add(2*time.Minute))
	pending, result = Enqueue(pending, lower, now)
	if result.Added {
		t.Errorf("result = %+v, want the lower alert suppressed", result)
	}
	if len(pending) != 1 || pending[0].Level != 90 {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestEnqueueSuppressesDuplicateAnomaly(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	first := notification(persistence.NotificationAnomaly6H, "2026-11", 0, now)
	pending, _ := Enqueue(nil, first, now)
	second := notification(persistence.NotificationAnomaly6H, "2026-11", 0, now.Add(time.Hour))
	pending, result := Enqueue(pending, second, now)
	if result.Added || !result.Collapsed {
		t.Errorf("result = %+v", result)
	}
	if len(pending) != 1 || !Same(pending[0], first) {
		t.Errorf("pending = %+v", pending)
	}
}

func TestEnqueueDoesNotMergeDifferentKindsOrPeriods(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	pending, _ := Enqueue(nil, notification(persistence.NotificationMonthlyThreshold, "2026-11", 50, now), now)
	pending, _ = Enqueue(pending, notification(persistence.NotificationAnomaly6H, "2026-11", 0, now), now)
	pending, _ = Enqueue(pending, notification(persistence.NotificationMonthlyThreshold, "2026-10", 50, now), now)
	if len(pending) != 3 {
		t.Fatalf("pending = %+v, want 3 entries", pending)
	}
}

func TestEnqueueDropsOldestWhenFull(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	var pending []persistence.Notification
	for i := 0; i < MaxPending; i++ {
		pending = append(pending, notification(
			persistence.NotificationAnomaly6H, "2026-11", 0, now.Add(time.Duration(i)*time.Minute)))
	}
	newest := notification(persistence.NotificationAnomaly6H, "2026-10", 0, now.Add(time.Hour))
	pending, result := Enqueue(pending, newest, now)
	if len(pending) != MaxPending {
		t.Fatalf("pending = %d, want %d", len(pending), MaxPending)
	}
	if len(result.DroppedOverflow) != 1 {
		t.Fatalf("dropped = %+v, want one entry", result.DroppedOverflow)
	}
	if !result.DroppedOverflow[0].CreatedAt.Equal(now) {
		t.Errorf("dropped the wrong entry: %+v", result.DroppedOverflow[0])
	}
}

func TestRemove(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	first := notification(persistence.NotificationMonthlyThreshold, "2026-11", 50, now)
	second := notification(persistence.NotificationAnomaly6H, "2026-11", 0, now)
	pending := []persistence.Notification{first, second}

	pending = Remove(pending, first)
	if len(pending) != 1 || !Same(pending[0], second) {
		t.Fatalf("pending = %+v", pending)
	}
	if got := Remove(pending, first); len(got) != 1 {
		t.Errorf("Remove(missing) changed the queue: %+v", got)
	}
}

func TestDropKind(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	pending := []persistence.Notification{
		notification(persistence.NotificationMonthlyThreshold, "2026-10", 50, now),
		notification(persistence.NotificationAnomaly6H, "2026-10", 0, now),
		notification(persistence.NotificationMonthlyThreshold, "2026-11", 75, now),
	}
	kept, dropped := DropKind(pending, persistence.NotificationMonthlyThreshold)
	if len(dropped) != 2 {
		t.Errorf("dropped = %d, want 2", len(dropped))
	}
	if len(kept) != 1 || kept[0].Kind != persistence.NotificationAnomaly6H {
		t.Errorf("kept = %+v", kept)
	}
}

func TestEnqueueExpiresOldEntries(t *testing.T) {
	now := utc(2026, 11, 7, 13)
	old := notification("anomaly_6h", "2026-10", 0, now.Add(-8*24*time.Hour))
	fresh := notification("anomaly_6h", "2026-11", 0, now)
	pending, result := Enqueue([]persistence.Notification{old}, fresh, now)
	if len(pending) != 1 || !pending[0].CreatedAt.Equal(fresh.CreatedAt) {
		t.Errorf("pending = %+v, want only the fresh entry", pending)
	}
	if len(result.DroppedExpired) != 1 {
		t.Errorf("DroppedExpired = %+v, want the old entry", result.DroppedExpired)
	}
}
