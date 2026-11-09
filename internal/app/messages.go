package app

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/alerts"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// notBillableNote is part of every message: the VNIC counter is registered
// traffic, not the billable one.
const notBillableNote = "nota: es tráfico registrado, no el tráfico facturable."

const (
	measurementMonthly    = "tráfico saliente de red (VnicToNetworkBytes, suma horaria)"
	measurementAnomaly    = "tráfico saliente en las últimas 6 horas completas"
	measurementCollection = "consultas a OCI Monitoring (namespace oci_vcn)"
	measurementTest       = "prueba de configuración de Telegram"
)

// RenderAlert renders one evaluated alert in Spanish. The alert already carries
// the period and the measured values; the header adds the instance and region.
func RenderAlert(cfg config.Config, alert alerts.Alert, now time.Time) string {
	switch {
	case alert.Monthly != nil:
		return renderMonthly(cfg, alert.Monthly, alert.CreatedAt)
	case alert.Anomaly != nil:
		return renderAnomaly(cfg, alert.Anomaly, alert.CreatedAt)
	default:
		return ""
	}
}

func renderMonthly(cfg config.Config, monthly *alerts.MonthlyAlert, evaluated time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OCI Infrastructure Monitor — alerta mensual (%s)\n", monthly.Severity)
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", monthly.Month)
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(evaluated))
	fmt.Fprintf(&b, "medición: %s\n", measurementMonthly)
	fmt.Fprintf(&b, "consumo acumulado: %s (%s bytes)\n", formatGiB(monthly.UsageBytes), formatThousands(monthly.UsageBytes))
	fmt.Fprintf(&b, "límite configurado: %s (%s bytes)\n", formatGiB(monthly.LimitBytes), formatThousands(monthly.LimitBytes))
	fmt.Fprintf(&b, "porcentaje consumido: %s %% (truncado a un decimal)\n", monthly.Percentage)
	fmt.Fprintf(&b, "severidad: %s\n", monthly.Severity)
	if len(monthly.Crossed) > 0 {
		fmt.Fprintf(&b, "umbrales cruzados en esta alerta: %s\n", formatPercentList(monthly.Crossed))
	}
	for _, warning := range monthly.Warnings {
		fmt.Fprintf(&b, "AVISO: %s\n", warning)
	}
	b.WriteString(notBillableNote)
	return b.String()
}

func renderAnomaly(cfg config.Config, anomaly *alerts.AnomalyAlert, evaluated time.Time) string {
	var b strings.Builder
	b.WriteString("OCI Infrastructure Monitor — consumo anormal\n")
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", anomaly.Month)
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(evaluated))
	fmt.Fprintf(&b, "medición: %s\n", measurementAnomaly)
	fmt.Fprintf(&b, "ventana: %s a %s\n", formatTimestamp(anomaly.Window.Start), formatTimestamp(anomaly.Window.End))
	fmt.Fprintf(&b, "consumo acumulado: %s (%s bytes) en %d horas\n",
		formatGiB(anomaly.Window.Bytes), formatThousands(anomaly.Window.Bytes), anomaly.Window.Hours())
	fmt.Fprintf(&b, "límite configurado: %s (%s bytes) en %d horas\n",
		formatGiB(anomaly.ThresholdBytes), formatThousands(anomaly.ThresholdBytes), anomaly.Window.Hours())
	fmt.Fprintf(&b, "porcentaje consumido: %s %% del umbral de 6 h (truncado a un decimal)\n",
		formatPercentage(anomaly.Window.Bytes, anomaly.ThresholdBytes))
	if anomaly.Reliable {
		b.WriteString("fiabilidad: la ventana no tiene avisos de calidad de datos\n")
	} else {
		b.WriteString("fiabilidad: la ventana tiene datos provisionales o incompletos; la cifra no es una conclusión firme\n")
	}
	b.WriteString("severidad: consumo anormal\n")
	for _, warning := range anomaly.Warnings {
		fmt.Fprintf(&b, "AVISO: %s\n", warning)
	}
	b.WriteString(notBillableNote)
	return b.String()
}

// RenderCollectionFailure renders the persistent-collection-error alert. The
// monthly figures are included as context and are explicitly marked as
// possibly incomplete.
func RenderCollectionFailure(cfg config.Config, summary accumulate.Summary, now, since, last time.Time, failures, threshold int) string {
	var b strings.Builder
	b.WriteString("OCI Infrastructure Monitor — error persistente de recopilación\n")
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", persistence.FormatMonth(now))
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(now))
	fmt.Fprintf(&b, "medición: %s\n", measurementCollection)
	fmt.Fprintf(&b, "fallos consecutivos: %d (umbral de alerta: %d)\n", failures, threshold)
	fmt.Fprintf(&b, "rango de la falla: %s\n", formatRange(since, last))
	writeMonthlyContext(&b, cfg, summary)
	b.WriteString("severidad: error persistente de recopilación\n")
	b.WriteString("AVISO: el acumulado del mes puede estar incompleto porque algunas consultas a OCI fallaron o una VNIC nunca fue visible.\n")
	b.WriteString(notBillableNote)
	return b.String()
}

// RenderCollectionRecovery renders the recovery that follows a delivered
// collection error. It quotes the failure range so it is not a context-free
// "everything is fine" message.
func RenderCollectionRecovery(cfg config.Config, summary accumulate.Summary, now, since, last time.Time) string {
	var b strings.Builder
	b.WriteString("OCI Infrastructure Monitor — recuperación del funcionamiento normal\n")
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", persistence.FormatMonth(now))
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(now))
	fmt.Fprintf(&b, "medición: %s\n", measurementCollection)
	fmt.Fprintf(&b, "recuperado el: %s\n", formatTimestamp(now))
	fmt.Fprintf(&b, "rango de la falla: %s\n", formatRange(since, last))
	b.WriteString("fallos consecutivos: 0\n")
	writeMonthlyContext(&b, cfg, summary)
	b.WriteString("severidad: recuperación del funcionamiento normal\n")
	b.WriteString(notBillableNote)
	return b.String()
}

// RenderTestMessage renders the explicit test message sent by test-telegram.
func RenderTestMessage(cfg config.Config, summary accumulate.Summary, now time.Time) string {
	var b strings.Builder
	b.WriteString("OCI Infrastructure Monitor — mensaje de prueba\n")
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", persistence.FormatMonth(now))
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(now))
	fmt.Fprintf(&b, "medición: %s\n", measurementTest)
	writeMonthlyContext(&b, cfg, summary)
	b.WriteString("severidad: informativa\n")
	b.WriteString("este mensaje confirma que el bot puede publicar en el chat configurado.\n")
	b.WriteString(notBillableNote)
	return b.String()
}

// MonthStateLabel describes the lifecycle state of a month in Spanish. status
// and the daily digest share it so both read the same.
func MonthStateLabel(month MonthStatus) string {
	switch {
	case month.Err != nil:
		return "error de lectura"
	case !month.HasHistory:
		return "sin datos"
	case month.Finalized && month.FinalizedAt != nil:
		return "finalizado el " + formatTimestamp(*month.FinalizedAt)
	case month.Finalized:
		return "finalizado"
	case month.Grace:
		return "no finalizado (en gracia)"
	case month.PendingFinalization:
		return "no finalizado, pendiente de finalización"
	default:
		return "abierto"
	}
}

// AccumulatedLabel describes the accumulator of a month. The outbound
// accumulator is the one the roadmap tracks; inbound is only used when the
// month has no outbound bucket at all, as status has always done.
func AccumulatedLabel(summary accumulate.Summary) string {
	if !summary.HasData {
		return "sin datos"
	}
	direction := summary.Outbound
	if direction.Buckets == 0 {
		direction = summary.Inbound
	}
	return accumulatedLabel(direction)
}

func accumulatedLabel(direction accumulate.DirectionSummary) string {
	switch direction.Status {
	case persistence.StatusComplete:
		return "completo"
	case persistence.StatusProvisional:
		if direction.Reason != "" {
			return "provisional (" + direction.Reason + ")"
		}
		return "provisional"
	case persistence.StatusIncomplete:
		if direction.Reason != "" {
			return "incompleto (" + direction.Reason + ")"
		}
		return "incompleto"
	default:
		return string(direction.Status)
	}
}

// RenderPending returns the stored text of a queued notification. A version 2
// state could not contain queued notifications, but if a migrated state has one
// without text this function rebuilds the closest possible message from the
// kind, period and level.
func RenderPending(cfg config.Config, notification persistence.Notification, summary accumulate.Summary, now time.Time) string {
	if strings.TrimSpace(notification.Text) != "" {
		return notification.Text
	}
	switch notification.Kind {
	case persistence.NotificationMonthlyThreshold:
		limit := bytesFromGiB(cfg.Network.MonthlyLimitGiB)
		monthly := &alerts.MonthlyAlert{
			Month:             notification.Period,
			Severity:          severityForPercentage(cfg.Network.AlertPercentages, notification.Level),
			Threshold:         notification.Level,
			Crossed:           crossedUpTo(cfg.Network.AlertPercentages, notification.Level),
			UsageBytes:        summary.Outbound.Bytes,
			LimitBytes:        limit,
			Percentage:        formatPercentage(summary.Outbound.Bytes, limit),
			AccumulatedStatus: summary.Outbound.Status,
			Warnings:          summaryWarnings(summary.Outbound),
		}
		return renderMonthly(cfg, monthly, notification.CreatedAt)
	case persistence.NotificationAnomaly6H:
		return renderPendingGeneric(cfg, notification, summary, now, "consumo anormal")
	default:
		return renderPendingGeneric(cfg, notification, summary, now, notification.Kind)
	}
}

func renderPendingGeneric(cfg config.Config, notification persistence.Notification, summary accumulate.Summary, now time.Time, severity string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OCI Infrastructure Monitor — notificación pendiente (%s)\n", severity)
	writeHeader(&b, cfg)
	fmt.Fprintf(&b, "período: %s\n", notification.Period)
	fmt.Fprintf(&b, "fecha de evaluación: %s\n", formatTimestamp(notification.CreatedAt))
	fmt.Fprintf(&b, "medición: %s\n", measurementMonthly)
	writeMonthlyContext(&b, cfg, summary)
	fmt.Fprintf(&b, "severidad: %s\n", severity)
	b.WriteString(notBillableNote)
	return b.String()
}

// AddPreviousPeriodNotice prepends the warning that a queued notification was
// generated for an earlier month. Without it a delayed alert would look like a
// message about the month in progress.
func AddPreviousPeriodNotice(text string, notification persistence.Notification, now time.Time) string {
	currentMonth := persistence.FormatMonth(now)
	if notification.Period == "" || notification.Period == currentMonth {
		return text
	}
	notice := fmt.Sprintf(
		"AVISO: esta notificación se generó el %s para el período %s; no corresponde al mes en curso (%s).\n",
		formatTimestamp(notification.CreatedAt), notification.Period, currentMonth)
	return notice + text
}

func writeHeader(b *strings.Builder, cfg config.Config) {
	instance := strings.TrimSpace(cfg.Network.InstanceOCID)
	if instance == "" {
		instance = "no configurada"
	}
	region := strings.TrimSpace(cfg.OCI.Region)
	if region == "" {
		region = "no configurada"
	}
	fmt.Fprintf(b, "instancia: %s\n", instance)
	fmt.Fprintf(b, "región: %s\n", region)
}

func writeMonthlyContext(b *strings.Builder, cfg config.Config, summary accumulate.Summary) {
	limit := bytesFromGiB(cfg.Network.MonthlyLimitGiB)
	if summary.Outbound.Buckets == 0 {
		b.WriteString("consumo acumulado: sin datos del mes en curso\n")
		if limit > 0 {
			fmt.Fprintf(b, "límite configurado: %s (%s bytes)\n", formatGiB(limit), formatThousands(limit))
		}
		b.WriteString("porcentaje consumido: no disponible\n")
		return
	}
	fmt.Fprintf(b, "consumo acumulado: %s (%s bytes)\n", formatGiB(summary.Outbound.Bytes), formatThousands(summary.Outbound.Bytes))
	if limit > 0 {
		fmt.Fprintf(b, "límite configurado: %s (%s bytes)\n", formatGiB(limit), formatThousands(limit))
		fmt.Fprintf(b, "porcentaje consumido: %s %% (truncado a un decimal)\n", formatPercentage(summary.Outbound.Bytes, limit))
	} else {
		b.WriteString("límite configurado: no configurado\n")
		b.WriteString("porcentaje consumido: no disponible\n")
	}
}

func summaryWarnings(direction accumulate.DirectionSummary) []string {
	switch direction.Status {
	case persistence.StatusIncomplete:
		reason := direction.Reason
		if reason == "" {
			reason = "hay datos no recuperados"
		}
		return []string{fmt.Sprintf("el acumulado es incompleto (%s); el consumo real puede ser mayor", reason)}
	case persistence.StatusProvisional:
		reason := direction.Reason
		if reason == "" {
			reason = "hay horas recientes aún no confirmadas"
		}
		return []string{fmt.Sprintf("el acumulado es provisional (%s); los datos recientes todavía pueden cambiar", reason)}
	default:
		return nil
	}
}

func severityForPercentage(percentages []int, level int) alerts.Severity {
	severity := alerts.SeverityInfo
	for i, percentage := range percentages {
		if percentage <= level && i < int(alerts.SeverityLimit) {
			severity = alerts.Severity(i + 1)
		}
	}
	return severity
}

func crossedUpTo(percentages []int, level int) []int {
	var crossed []int
	for _, percentage := range percentages {
		if percentage > 0 && percentage <= level {
			crossed = append(crossed, percentage)
		}
	}
	return crossed
}

func bytesFromGiB(value float64) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value > float64(math.MaxInt64)/float64(1<<30) {
		return math.MaxInt64
	}
	return int64(math.Round(value * float64(1<<30)))
}

func formatGiB(bytes int64) string {
	value := float64(bytes) / float64(1<<30)
	text := strconv.FormatFloat(value, 'f', 2, 64)
	return strings.Replace(text, ".", ",", 1) + " GiB"
}

func formatThousands(value int64) string {
	digits := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = strings.TrimPrefix(digits, "-")
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3 + 1)
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte('.')
		}
		out.WriteByte(digits[i])
	}
	if negative {
		return "-" + out.String()
	}
	return out.String()
}

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

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return "no disponible"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatRange(since, last time.Time) string {
	if since.IsZero() {
		return "no disponible"
	}
	if last.IsZero() || last.Before(since) {
		return formatTimestamp(since)
	}
	return formatTimestamp(since) + " a " + formatTimestamp(last)
}
