package alerts

import (
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// Input is the read-only snapshot the evaluators inspect. Histories must cover
// the months being evaluated: the current month for the monthly thresholds and
// the current and previous month for the six-hour window when it crosses a
// month boundary. The run command loads exactly those histories before calling
// Evaluate.
type Input struct {
	// Now is the evaluation instant; zero means no evaluation is possible.
	Now time.Time
	// Config is the validated configuration.
	Config config.Config
	// State is the persisted state, including the alerts already notified. The
	// evaluators never mutate it.
	State persistence.State
	// Histories maps "YYYY-MM" to the month history.
	Histories map[string]*persistence.History
}

// Evaluator proposes alerts from the persisted data. It performs no I/O, sends
// nothing and writes nothing.
type Evaluator interface {
	// Name identifies the evaluator in logs.
	Name() string
	// Evaluate returns the alerts that the current data and state justify.
	Evaluate(Input) []Alert
}

// Result is the outcome of one evaluation pass.
type Result struct {
	Alerts []Alert
}

// DefaultEvaluators returns the v1 evaluators in evaluation order.
func DefaultEvaluators() []Evaluator {
	return []Evaluator{MonthlyThresholds{}, Anomaly6H{}}
}

// Evaluate runs the evaluators over the snapshot. With no explicit evaluators it
// uses DefaultEvaluators. It is deterministic and safe to call more than once
// with the same input.
func Evaluate(in Input, evaluators ...Evaluator) Result {
	if len(evaluators) == 0 {
		evaluators = DefaultEvaluators()
	}
	in.Now = in.Now.UTC()
	var result Result
	for _, evaluator := range evaluators {
		if evaluator == nil {
			continue
		}
		result.Alerts = append(result.Alerts, evaluator.Evaluate(in)...)
	}
	return result
}

// MonthlyThresholds evaluates the four monthly alert_percentages over the
// outbound accumulated bytes of the current month. It never re-evaluates a
// finalized month and never evaluates a past month: corrections during the
// grace period update the total but do not raise alerts.
type MonthlyThresholds struct{}

// Name implements Evaluator.
func (MonthlyThresholds) Name() string { return "monthly_thresholds" }

// Evaluate implements Evaluator.
func (MonthlyThresholds) Evaluate(in Input) []Alert {
	if !in.Config.Network.Enabled {
		return nil
	}
	now := in.Now.UTC()
	month := persistence.FormatMonth(now)
	history := in.Histories[month]
	if history == nil || history.Finalized {
		return nil
	}

	summary := accumulate.Summarize(history, accumulate.OptionsFromConfig(in.Config))
	if summary.Outbound.Buckets == 0 {
		return nil
	}
	limitBytes := bytesFromGiB(in.Config.Network.MonthlyLimitGiB)
	if limitBytes <= 0 {
		return nil
	}

	crossed := crossedThresholds(summary.Outbound.Bytes, limitBytes, in.Config.Network.AlertPercentages)
	newlyCrossed := difference(crossed, in.State.Alerts.MonthlyAlerts[month])
	if len(newlyCrossed) == 0 {
		return nil
	}

	highest := newlyCrossed[len(newlyCrossed)-1]
	severity := severityFor(in.Config.Network.AlertPercentages, highest)
	if !severity.Valid() {
		return nil
	}
	// A delivery that failed keeps the alert in the queue. Do not propose a
	// duplicate while an equal or higher monthly alert for the period is still
	// pending; the run command will retry it before evaluating new alerts.
	if hasPendingMonthly(in.State, month, highest) {
		return nil
	}

	return []Alert{{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    month,
		Level:     highest,
		CreatedAt: now,
		Monthly: &MonthlyAlert{
			Month:             month,
			Severity:          severity,
			Threshold:         highest,
			Crossed:           newlyCrossed,
			UsageBytes:        summary.Outbound.Bytes,
			LimitBytes:        limitBytes,
			Percentage:        formatPercentage(summary.Outbound.Bytes, limitBytes),
			AccumulatedStatus: summary.Outbound.Status,
			Warnings:          monthlyWarnings(summary.Outbound),
		},
	}}
}

// Anomaly6H evaluates the outbound bytes of the last six complete hours
// against high_traffic_6h_gib, with a configurable cooldown measured from the
// last successfully delivered alert. A window with provisional, incomplete or
// missing buckets still fires when the threshold is exceeded, but the alert
// carries the warnings and says that no firm conclusion is possible.
//
// A future historical baseline can be added as another Evaluator over the same
// buckets; this evaluator deliberately does not assume a universal normal
// consumption.
type Anomaly6H struct{}

// Name implements Evaluator.
func (Anomaly6H) Name() string { return "anomaly_6h" }

// Evaluate implements Evaluator.
func (Anomaly6H) Evaluate(in Input) []Alert {
	if !in.Config.Network.Enabled || len(in.Config.Network.VNICOCIDs) == 0 {
		return nil
	}
	now := in.Now.UTC()
	threshold := bytesFromGiB(in.Config.Network.HighTraffic6HGiB)
	if threshold <= 0 {
		return nil
	}

	stats := windowStats(now, in.Config.Network.VNICOCIDs, in.Histories)
	if stats.Buckets == 0 || stats.Bytes < threshold {
		return nil
	}

	opts := accumulate.OptionsFromConfig(in.Config)
	warnings := anomalyWarnings(stats, opts)

	cooldown := time.Duration(in.Config.Network.HighTrafficCooldownHours) * time.Hour
	if cooldown <= 0 {
		cooldown = 24 * time.Hour
	}
	if previous := in.State.Alerts.Anomaly6H; previous != nil && previous.LastNotifiedAt != nil {
		if now.Before(previous.LastNotifiedAt.UTC().Add(cooldown)) {
			return nil
		}
	}

	period := persistence.FormatMonth(stats.End.Add(-time.Hour))
	if hasPendingAnomaly(in.State, period) {
		return nil
	}
	return []Alert{{
		Kind:      persistence.NotificationAnomaly6H,
		Period:    period,
		CreatedAt: now,
		Anomaly: &AnomalyAlert{
			Month:          period,
			Window:         stats,
			ThresholdBytes: threshold,
			Warnings:       warnings,
			Reliable:       len(warnings) == 0,
		},
	}}
}

// windowStats sums the outbound buckets of the last six complete hours. The
// window is [floor(now) - 6h, floor(now)): an hour that has not ended yet never
// counts, and an hour that ends exactly at now counts.
func windowStats(now time.Time, vnicIDs []string, histories map[string]*persistence.History) WindowStats {
	end := now.UTC().Truncate(time.Hour)
	start := end.Add(-WindowHours * time.Hour)
	stats := WindowStats{Start: start, End: end}

	for hour := start; hour.Before(end); hour = hour.Add(time.Hour) {
		history := histories[persistence.FormatMonth(hour)]
		for _, id := range vnicIDs {
			if history == nil {
				stats.Missing++
				continue
			}
			bucket, ok := history.Get(id, persistence.DirectionOutbound, hour)
			if !ok {
				stats.Missing++
				continue
			}
			stats.Buckets++
			stats.Bytes += bucket.Bytes
			switch bucket.Status {
			case persistence.StatusComplete:
				stats.Complete++
			case persistence.StatusProvisional:
				stats.Provisional++
			case persistence.StatusNoData:
				stats.NoData++
			case persistence.StatusIncomplete:
				stats.Incomplete++
			}
		}
	}
	stats.MaxNoDataStreak = maxNoDataStreak(start, end, vnicIDs, histories)
	return stats
}

func maxNoDataStreak(start, end time.Time, vnicIDs []string, histories map[string]*persistence.History) int {
	longest := 0
	for _, id := range vnicIDs {
		streak := 0
		for hour := start; hour.Before(end); hour = hour.Add(time.Hour) {
			history := histories[persistence.FormatMonth(hour)]
			if history == nil {
				streak = 0
				continue
			}
			bucket, ok := history.Get(id, persistence.DirectionOutbound, hour)
			if !ok || bucket.Status != persistence.StatusNoData {
				streak = 0
				continue
			}
			streak++
			if streak > longest {
				longest = streak
			}
		}
	}
	return longest
}

// crossedThresholds returns, ascending, the configured percentages whose exact
// byte threshold the usage has reached.
func crossedThresholds(usage, limitBytes int64, percentages []int) []int {
	var crossed []int
	for _, percentage := range percentages {
		if usage >= percentageBytes(limitBytes, percentage) {
			crossed = append(crossed, percentage)
		}
	}
	return crossed
}

// difference returns the values of crossed that are not in notified,
// preserving the order of crossed.
func difference(crossed, notified []int) []int {
	if len(crossed) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(notified))
	for _, value := range notified {
		seen[value] = struct{}{}
	}
	var out []int
	for _, value := range crossed {
		if _, ok := seen[value]; ok {
			continue
		}
		out = append(out, value)
	}
	return out
}

// severityFor returns the severity assigned by position to a percentage.
func severityFor(percentages []int, percentage int) Severity {
	for i, value := range percentages {
		if value == percentage && i < int(SeverityLimit) {
			return Severity(i + 1)
		}
	}
	return 0
}

func monthlyWarnings(direction accumulate.DirectionSummary) []string {
	switch direction.Status {
	case persistence.StatusIncomplete:
		detail := direction.Reason
		if detail == "" {
			detail = "hay datos no recuperados"
		}
		return []string{fmt.Sprintf("el acumulado es incompleto (%s); el consumo real puede ser mayor y la alerta se basa en datos parciales", detail)}
	case persistence.StatusProvisional:
		detail := direction.Reason
		if detail == "" {
			detail = "hay horas recientes aún no confirmadas"
		}
		return []string{fmt.Sprintf("el acumulado es provisional (%s); los datos recientes todavía pueden cambiar", detail)}
	default:
		return nil
	}
}

func anomalyWarnings(stats WindowStats, opts accumulate.Options) []string {
	var warnings []string
	if stats.Incomplete > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"la ventana contiene %s; el consumo real puede ser mayor y la cifra no permite conclusiones firmes",
			bucketCount(stats.Incomplete, "incompleto", "incompletos")))
	}
	if stats.Provisional > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"la ventana contiene %s; la cifra todavía no es definitiva",
			bucketCount(stats.Provisional, "provisional", "provisionales")))
	}
	if stats.Missing == 1 {
		warnings = append(warnings, "falta 1 bucket de las VNIC configuradas; la ventana está incompleta")
	} else if stats.Missing > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"faltan %d buckets de las VNIC configuradas; la ventana está incompleta", stats.Missing))
	}
	if opts.PublishZeros {
		streakHours := 6 // same default as accumulate.Options.noDataStreakHours
		if opts.NoDataStreak > 0 {
			streakHours = int(opts.NoDataStreak / time.Hour)
		}
		if streakHours < 1 {
			streakHours = 1
		}
		if stats.MaxNoDataStreak > streakHours {
			warnings = append(warnings, fmt.Sprintf(
				"la ventana contiene una racha de %d h sin datos (no_data) y OCI publica ceros explícitos; el consumo podría estar subestimado",
				stats.MaxNoDataStreak))
		}
	}
	return warnings
}

// hasPendingMonthly reports whether the queue already holds a monthly alert for
// the period at the same or a higher threshold percentage.
func hasPendingMonthly(state persistence.State, period string, threshold int) bool {
	for _, notification := range state.Pending {
		if notification.Kind != persistence.NotificationMonthlyThreshold || notification.Period != period {
			continue
		}
		if notification.Level >= threshold {
			return true
		}
	}
	return false
}

// hasPendingAnomaly reports whether the queue already holds an anomaly alert for
// the period.
func hasPendingAnomaly(state persistence.State, period string) bool {
	for _, notification := range state.Pending {
		if notification.Kind == persistence.NotificationAnomaly6H && notification.Period == period {
			return true
		}
	}
	return false
}

func bucketCount(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d bucket %s", n, singular)
	}
	return fmt.Sprintf("%d buckets %s", n, plural)
}
