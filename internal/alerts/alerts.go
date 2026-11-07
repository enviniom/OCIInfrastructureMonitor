// Package alerts evaluates the persisted hourly buckets and state and proposes
// notifications. It performs no I/O, sends nothing and writes no state: the run
// command decides when to enqueue, deliver and mark an alert as notified. This
// keeps the "mark as sent only after a successful delivery" rule of the
// Telegram step outside the evaluators.
package alerts

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// WindowHours is the number of complete hours the abnormal-consumption
// evaluator sums. The roadmap fixes it at six hours.
const WindowHours = 6

// Severity is the fixed position-based severity of the monthly thresholds:
// position 1 is informative, 2 warning, 3 critical and 4 the limit reached.
type Severity int

const (
	// SeverityInfo is the first configured threshold: informative.
	SeverityInfo Severity = iota + 1
	// SeverityWarning is the second threshold: warning.
	SeverityWarning
	// SeverityCritical is the third threshold: critical.
	SeverityCritical
	// SeverityLimit is the fourth threshold, always 100: limit reached.
	SeverityLimit
)

// Valid reports whether the severity is one of the four fixed levels.
func (s Severity) Valid() bool {
	return s >= SeverityInfo && s <= SeverityLimit
}

// String returns the Spanish name of the severity.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "informativa"
	case SeverityWarning:
		return "advertencia"
	case SeverityCritical:
		return "crítica"
	case SeverityLimit:
		return "límite alcanzado"
	default:
		return "desconocida"
	}
}

// Alert is one proposed notification. Exactly one of Monthly or Anomaly is set.
type Alert struct {
	// Kind is one of the persistence notification kinds.
	Kind string
	// Period is the month the alert belongs to ("YYYY-MM").
	Period string
	// Level is the highest crossed threshold percentage for monthly alerts
	// (for example 90) and zero for the others. The pending queue collapses
	// monthly alerts by (period, kind) at the highest level, so 90 wins over
	// 75. Severity is the position-based read in MonthlyAlert.Severity.
	Level int
	// CreatedAt is the evaluation instant.
	CreatedAt time.Time
	// Monthly is set for a monthly threshold alert.
	Monthly *MonthlyAlert
	// Anomaly is set for an abnormal-consumption alert.
	Anomaly *AnomalyAlert
}

// MonthlyAlert is a crossed monthly threshold.
type MonthlyAlert struct {
	Month     string
	Severity  Severity
	Threshold int
	// Crossed lists the thresholds crossed since the previous notified alert,
	// ascending. All of them are recorded when the alert is delivered, so a
	// later alert only reports the new ones.
	Crossed    []int
	UsageBytes int64
	LimitBytes int64
	// Percentage is the consumed percentage truncated to one decimal, with a
	// decimal comma (Spanish convention).
	Percentage        string
	AccumulatedStatus persistence.Status
	// Warnings explain that the accumulated total is incomplete or
	// provisional. An alert with warnings is still valid but must say so.
	Warnings []string
}

// WindowStats describes the six complete hours summed by the anomaly evaluator.
type WindowStats struct {
	// Start is inclusive and End is exclusive.
	Start time.Time
	End   time.Time
	Bytes int64
	// Buckets is the number of buckets found; Missing counts the configured
	// VNIC/hour pairs without a bucket.
	Buckets     int
	Missing     int
	Complete    int
	Provisional int
	NoData      int
	Incomplete  int
	// MaxNoDataStreak is the longest run of consecutive no_data hours of one
	// VNIC inside the window.
	MaxNoDataStreak int
}

// Hours returns the number of hours covered by the window.
func (w WindowStats) Hours() int {
	if !w.End.After(w.Start) {
		return 0
	}
	return int(w.End.Sub(w.Start) / time.Hour)
}

// AnomalyAlert is an abnormal outbound consumption over the last six complete
// hours.
type AnomalyAlert struct {
	Month          string
	Window         WindowStats
	ThresholdBytes int64
	// Warnings explain why the window cannot be read as a firm conclusion.
	Warnings []string
	// Reliable is true when no warning applies.
	Reliable bool
}

// Notification returns the queue entry that the run command should persist when
// it decides to enqueue the alert. The delivery state is handled by the queue,
// not by the evaluator.
func (a Alert) Notification() persistence.Notification {
	notification := persistence.Notification{
		Kind:      a.Kind,
		Period:    a.Period,
		Level:     a.Level,
		CreatedAt: a.CreatedAt.UTC(),
	}
	if a.Monthly != nil {
		notification.Period = a.Monthly.Month
		notification.Level = a.Monthly.Threshold
	}
	return notification
}

// Text renders the alert in Spanish. The run command adds the instance and
// region header and the Telegram-specific wording.
func (a Alert) Text() string {
	switch {
	case a.Monthly != nil:
		return a.Monthly.Text()
	case a.Anomaly != nil:
		return a.Anomaly.Text()
	default:
		return ""
	}
}

// Title returns the Spanish title of a monthly alert.
func (m MonthlyAlert) Title() string {
	return "Alerta mensual — " + m.Severity.String()
}

// Text renders the monthly alert in Spanish.
func (m MonthlyAlert) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", m.Title())
	fmt.Fprintf(&b, "período: %s\n", m.Month)
	fmt.Fprintf(&b, "consumo acumulado: %s (%s bytes) de %s\n",
		formatGiB(m.UsageBytes), formatThousands(m.UsageBytes), formatGiB(m.LimitBytes))
	fmt.Fprintf(&b, "porcentaje consumido: %s %% (truncado a un decimal)\n", m.Percentage)
	if len(m.Crossed) > 0 {
		fmt.Fprintf(&b, "umbrales cruzados en esta alerta: %s\n", formatPercentList(m.Crossed))
	}
	for _, warning := range m.Warnings {
		fmt.Fprintf(&b, "AVISO: %s\n", warning)
	}
	b.WriteString("nota: es tráfico registrado, no el tráfico facturable.")
	return b.String()
}

// Text renders the abnormal-consumption alert in Spanish.
func (a AnomalyAlert) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Alerta de consumo anormal — últimas %d horas completas\n", a.Window.Hours())
	fmt.Fprintf(&b, "ventana: %s UTC a %s UTC\n",
		formatTimestamp(a.Window.Start), formatTimestamp(a.Window.End))
	fmt.Fprintf(&b, "consumo saliente: %s (%s bytes) en %d horas; umbral: %s\n",
		formatGiB(a.Window.Bytes), formatThousands(a.Window.Bytes), a.Window.Hours(), formatGiB(a.ThresholdBytes))
	for _, warning := range a.Warnings {
		fmt.Fprintf(&b, "AVISO: %s\n", warning)
	}
	b.WriteString("nota: es tráfico registrado, no el tráfico facturable.")
	return b.String()
}

// MarkNotified records the bookkeeping of an alert that was delivered
// successfully. The run command must call it only after Telegram accepted the
// message; the evaluators never call it. Monthly thresholds are merged into the
// month (so 50 and 75 are recorded when the 90 alert is delivered) and the
// anomaly cooldown starts at at.
func MarkNotified(state *persistence.State, alert Alert, at time.Time) {
	if state == nil || at.IsZero() {
		return
	}
	at = at.UTC()
	switch alert.Kind {
	case persistence.NotificationMonthlyThreshold:
		if alert.Monthly == nil {
			return
		}
		if state.Alerts.MonthlyAlerts == nil {
			state.Alerts.MonthlyAlerts = map[string][]int{}
		}
		merged := append([]int(nil), state.Alerts.MonthlyAlerts[alert.Monthly.Month]...)
		merged = append(merged, alert.Monthly.Crossed...)
		if len(merged) == 0 {
			return
		}
		state.Alerts.MonthlyAlerts[alert.Monthly.Month] = uniqueSorted(merged)
	case persistence.NotificationAnomaly6H:
		last := at
		state.Alerts.Anomaly6H = &persistence.Anomaly6HState{LastNotifiedAt: &last}
	}
}

// MarkNotificationNotified applies the bookkeeping of a queued notification
// after Telegram accepted it. A pending monthly notification only stores its
// highest threshold percentage, so crossed records every configured percentage
// up to that level: delivering the 90 % alert records 50, 75 and 90 even if the
// earlier thresholds were collapsed into the queued message.
func MarkNotificationNotified(state *persistence.State, notification persistence.Notification, percentages []int, at time.Time) {
	switch notification.Kind {
	case persistence.NotificationMonthlyThreshold:
		var crossed []int
		for _, percentage := range percentages {
			if percentage > 0 && percentage <= notification.Level {
				crossed = append(crossed, percentage)
			}
		}
		MarkNotified(state, Alert{
			Kind:   persistence.NotificationMonthlyThreshold,
			Period: notification.Period,
			Monthly: &MonthlyAlert{
				Month:   notification.Period,
				Crossed: crossed,
			},
		}, at)
	case persistence.NotificationAnomaly6H:
		MarkNotified(state, Alert{Kind: persistence.NotificationAnomaly6H}, at)
	}
}

func uniqueSorted(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	out := sorted[:1]
	for _, value := range sorted[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// percentageBytes returns the exact byte count of percentage percent of limit,
// rounded up so that "used >= threshold" is equivalent to "used * 100 >=
// limit * percentage". It saturates at the maximum int64 for absurd limits.
func percentageBytes(limit int64, percentage int) int64 {
	if limit <= 0 || percentage <= 0 {
		return 0
	}
	value := new(big.Int).Mul(big.NewInt(limit), big.NewInt(int64(percentage)))
	value.Add(value, big.NewInt(99))
	value.Quo(value, big.NewInt(100))
	if !value.IsInt64() {
		return math.MaxInt64
	}
	return value.Int64()
}

// formatPercentage divides used by limit and truncates the result to one
// decimal, using a comma. Truncation is deliberate: 89.96 % must not be shown
// as 90 % when the 90 % alert has not fired.
func formatPercentage(used, limit int64) string {
	if limit <= 0 {
		return "0,0"
	}
	if used < 0 {
		used = 0
	}
	tenths := new(big.Int).Mul(big.NewInt(used), big.NewInt(1000))
	tenths.Quo(tenths, big.NewInt(limit))
	text := tenths.String()
	if len(text) == 1 {
		return "0," + text
	}
	return text[:len(text)-1] + "," + text[len(text)-1:]
}

func formatPercentList(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%d %%", value)
	}
	return strings.Join(parts, ", ")
}
