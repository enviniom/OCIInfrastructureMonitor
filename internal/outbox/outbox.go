// Package outbox implements the bounded queue of pending Telegram
// notifications persisted in state.json. It is pure: it never sends, never
// writes and never logs. The run command applies the result.
package outbox

import (
	"sort"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// Queue limits. The roadmap fixes both: 50 notifications and a 7 day lifetime.
const (
	MaxPending = 50
	TTL        = 7 * 24 * time.Hour
)

// Result reports what Enqueue changed so the caller can log it.
type Result struct {
	// Added is true when the queue gained a new or upgraded entry.
	Added bool
	// Collapsed is true when the new notification was folded into an existing
	// (kind, period) entry, either by upgrading the level or by being
	// suppressed as a duplicate.
	Collapsed bool
	// DroppedExpired and DroppedOverflow list the entries discarded to keep
	// the queue bounded and fresh.
	DroppedExpired  []persistence.Notification
	DroppedOverflow []persistence.Notification
}

// Expire removes the entries older than TTL. An entry exactly TTL old is
// expired: the queue only keeps notifications from the last seven days.
func Expire(pending []persistence.Notification, now time.Time) ([]persistence.Notification, []persistence.Notification) {
	cutoff := now.UTC().Add(-TTL)
	kept := make([]persistence.Notification, 0, len(pending))
	var expired []persistence.Notification
	for _, notification := range pending {
		if !notification.CreatedAt.After(cutoff) {
			expired = append(expired, notification)
			continue
		}
		kept = append(kept, notification)
	}
	return Sort(kept), expired
}

// Enqueue adds a notification, collapsing it by (kind, period) at the highest
// level. A monthly alert with a higher percentage replaces the queued entry so
// a single message reports the current level; the delivery records every
// threshold up to that level. An equal or lower monthly alert, a repeated
// anomaly or a repeated collection notification is kept as the existing entry.
func Enqueue(pending []persistence.Notification, notification persistence.Notification, now time.Time) ([]persistence.Notification, Result) {
	result := Result{}
	for i, existing := range pending {
		if existing.Kind != notification.Kind || existing.Period != notification.Period {
			continue
		}
		result.Collapsed = true
		if notification.Kind != persistence.NotificationMonthlyThreshold {
			return Sort(pending), result
		}
		if notification.Level <= existing.Level {
			return Sort(pending), result
		}
		updated := notification
		updated.Attempts = existing.Attempts
		updated.LastAttempt = existing.LastAttempt
		if updated.CreatedAt.IsZero() {
			updated.CreatedAt = now.UTC()
		}
		pending[i] = updated
		result.Added = true
		return Sort(pending), result
	}

	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = now.UTC()
	}
	pending = append(pending, notification)
	result.Added = true
	pending = Sort(pending)

	if len(pending) > MaxPending {
		overflow := len(pending) - MaxPending
		result.DroppedOverflow = append(result.DroppedOverflow, pending[:overflow]...)
		pending = append([]persistence.Notification(nil), pending[overflow:]...)
	}
	return pending, result
}

// Remove drops the first entry equal to target by kind, period, level and
// creation time. It returns the queue unchanged when the entry is not found.
func Remove(pending []persistence.Notification, target persistence.Notification) []persistence.Notification {
	for i, notification := range pending {
		if Same(notification, target) {
			out := make([]persistence.Notification, 0, len(pending)-1)
			out = append(out, pending[:i]...)
			out = append(out, pending[i+1:]...)
			return out
		}
	}
	return pending
}

// DropKind removes every entry of kind and returns the remaining queue together
// with the dropped entries. The run command uses it when telegram.enabled
// changes from false to true: the stale monthly alerts are collapsed and a new
// one is evaluated at the current level.
func DropKind(pending []persistence.Notification, kind string) ([]persistence.Notification, []persistence.Notification) {
	kept := make([]persistence.Notification, 0, len(pending))
	var dropped []persistence.Notification
	for _, notification := range pending {
		if notification.Kind == kind {
			dropped = append(dropped, notification)
			continue
		}
		kept = append(kept, notification)
	}
	return kept, dropped
}

// Same reports whether two queue entries identify the same notification.
func Same(a, b persistence.Notification) bool {
	return a.Kind == b.Kind && a.Period == b.Period && a.Level == b.Level && a.CreatedAt.Equal(b.CreatedAt)
}

// Sort returns a copy ordered by creation time, then kind, period and level, so
// the persisted state and the delivery order are deterministic.
func Sort(pending []persistence.Notification) []persistence.Notification {
	out := make([]persistence.Notification, len(pending))
	copy(out, pending)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Period != out[j].Period {
			return out[i].Period < out[j].Period
		}
		return out[i].Level < out[j].Level
	})
	return out
}
