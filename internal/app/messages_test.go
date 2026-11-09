package app

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/alerts"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func messageConfig() config.Config {
	cfg := testConfig()
	cfg.Network.MonthlyLimitGiB = 100
	cfg.Network.HighTraffic6HGiB = 10
	return cfg
}

func TestRenderMonthlyMessageHasEveryRequiredField(t *testing.T) {
	cfg := messageConfig()
	now := utcHour(2026, 11, 7, 13)
	limit := int64(100) << 30
	usage := int64(75) << 30
	alert := alerts.Alert{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		CreatedAt: now,
		Monthly: &alerts.MonthlyAlert{
			Month:      "2026-11",
			Severity:   alerts.SeverityWarning,
			Threshold:  75,
			Crossed:    []int{50, 75},
			UsageBytes: usage,
			LimitBytes: limit,
			Percentage: "75,0",
		},
	}
	text := RenderAlert(cfg, alert, now)
	for _, want := range []string{
		"OCI Infrastructure Monitor — alerta mensual (advertencia)",
		"instancia: " + cfg.Network.InstanceOCID,
		"región: us-ashburn-1",
		"período: 2026-11",
		"fecha de evaluación: 2026-11-07 13:00:00 UTC",
		"medición: tráfico saliente de red",
		"consumo acumulado: 75,00 GiB",
		"límite configurado: 100,00 GiB",
		"porcentaje consumido: 75,0 %",
		"severidad: advertencia",
		"umbrales cruzados en esta alerta: 50 %, 75 %",
		"nota: es tráfico registrado, no el tráfico facturable.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
}

func TestRenderAnomalyMessage(t *testing.T) {
	cfg := messageConfig()
	now := utcHour(2026, 11, 7, 13)
	alert := alerts.Alert{
		Kind:      persistence.NotificationAnomaly6H,
		Period:    "2026-11",
		CreatedAt: now,
		Anomaly: &alerts.AnomalyAlert{
			Month: "2026-11",
			Window: alerts.WindowStats{
				Start: now.Add(-6 * time.Hour),
				End:   now,
				Bytes: 15 << 30,
			},
			ThresholdBytes: 10 << 30,
			Warnings:       []string{"la ventana contiene 1 bucket provisional; la cifra todavía no es definitiva"},
			Reliable:       false,
		},
	}
	text := RenderAlert(cfg, alert, now)
	for _, want := range []string{
		"consumo anormal",
		"medición: tráfico saliente en las últimas 6 horas completas",
		"ventana: 2026-11-07 07:00:00 UTC a 2026-11-07 13:00:00 UTC",
		"consumo acumulado: 15,00 GiB",
		"límite configurado: 10,00 GiB",
		"porcentaje consumido: 150,0 %",
		"fiabilidad: la ventana tiene datos provisionales o incompletos",
		"severidad: consumo anormal",
		"AVISO: la ventana contiene 1 bucket provisional",
		"tráfico registrado, no el tráfico facturable",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
}

func TestRenderCollectionFailureAndRecovery(t *testing.T) {
	cfg := messageConfig()
	now := utcHour(2026, 11, 7, 13)
	since := now.Add(-72 * time.Hour)
	last := now.Add(-time.Hour)
	summary := accumulate.Summary{
		Month:   "2026-11",
		HasData: true,
		Outbound: accumulate.DirectionSummary{
			Direction:  persistence.DirectionOutbound,
			Bytes:      5 << 30,
			Buckets:    10,
			Incomplete: 2,
			Status:     persistence.StatusIncomplete,
			Reason:     "2 buckets incompletos",
		},
	}

	failure := RenderCollectionFailure(cfg, summary, now, since, last, 3, 3)
	for _, want := range []string{
		"error persistente de recopilación",
		"fallos consecutivos: 3 (umbral de alerta: 3)",
		"rango de la falla: 2026-11-04 13:00:00 UTC a 2026-11-07 12:00:00 UTC",
		"consumo acumulado: 5,00 GiB",
		"severidad: error persistente de recopilación",
		"AVISO: el acumulado del mes puede estar incompleto",
		"tráfico registrado, no el tráfico facturable",
	} {
		if !strings.Contains(failure, want) {
			t.Errorf("failure message missing %q:\n%s", want, failure)
		}
	}

	recovery := RenderCollectionRecovery(cfg, summary, now, since, last)
	for _, want := range []string{
		"recuperación del funcionamiento normal",
		"recuperado el: 2026-11-07 13:00:00 UTC",
		"rango de la falla: 2026-11-04 13:00:00 UTC a 2026-11-07 12:00:00 UTC",
		"fallos consecutivos: 0",
		"severidad: recuperación del funcionamiento normal",
	} {
		if !strings.Contains(recovery, want) {
			t.Errorf("recovery message missing %q:\n%s", want, recovery)
		}
	}
}

func TestRenderTestMessage(t *testing.T) {
	cfg := messageConfig()
	now := utcHour(2026, 11, 7, 13)
	text := RenderTestMessage(cfg, accumulate.Summary{Month: "2026-11"}, now)
	for _, want := range []string{
		"mensaje de prueba",
		"medición: prueba de configuración de Telegram",
		"consumo acumulado: sin datos del mes en curso",
		"severidad: informativa",
		"este mensaje confirma que el bot puede publicar",
		"tráfico registrado, no el tráfico facturable",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
}

func TestAddPreviousPeriodNotice(t *testing.T) {
	now := utcHour(2026, 11, 7, 13)
	notification := persistence.Notification{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-10",
		Level:     75,
		CreatedAt: utcHour(2026, 10, 31, 12),
		Text:      "cuerpo",
	}
	text := AddPreviousPeriodNotice(notification.Text, notification, now)
	if !strings.Contains(text, "AVISO: esta notificación se generó el 2026-10-31 12:00:00 UTC para el período 2026-10") {
		t.Errorf("missing previous-period notice:\n%s", text)
	}
	if !strings.Contains(text, "no corresponde al mes en curso (2026-11)") {
		t.Errorf("missing current month in notice:\n%s", text)
	}

	current := notification
	current.Period = "2026-11"
	if got := AddPreviousPeriodNotice("cuerpo", current, now); got != "cuerpo" {
		t.Errorf("current period got a notice: %q", got)
	}
}

func TestRenderPendingFallbackWithoutText(t *testing.T) {
	cfg := messageConfig()
	now := utcHour(2026, 11, 7, 13)
	notification := persistence.Notification{
		Kind:      persistence.NotificationMonthlyThreshold,
		Period:    "2026-11",
		Level:     75,
		CreatedAt: now.Add(-time.Hour),
	}
	summary := accumulate.Summary{
		Month:   "2026-11",
		HasData: true,
		Outbound: accumulate.DirectionSummary{
			Direction: persistence.DirectionOutbound,
			Bytes:     80 << 30,
			Buckets:   100,
			Complete:  100,
			Status:    persistence.StatusComplete,
		},
	}
	text := RenderPending(cfg, notification, summary, now)
	for _, want := range []string{
		"alerta mensual (advertencia)",
		"porcentaje consumido: 80,0 %",
		"umbrales cruzados en esta alerta: 50 %, 75 %",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q:\n%s", want, text)
		}
	}
}

func TestFormatPercentageTruncates(t *testing.T) {
	if got := formatPercentage(8996, 10000); got != "89,9" {
		t.Errorf("formatPercentage(8996,10000) = %q, want 89,9", got)
	}
	if got := formatPercentage(0, 100); got != "0,0" {
		t.Errorf("formatPercentage(0,100) = %q, want 0,0", got)
	}
	if got := formatPercentage(100, 100); got != "100,0" {
		t.Errorf("formatPercentage(100,100) = %q, want 100,0", got)
	}
}

func TestFormatPercentageForReportResolvesSmallValues(t *testing.T) {
	cases := []struct {
		used, limit int64
		want        string
	}{
		{0, 100, "0,0"},
		{8996, 10000, "89,9"},
		{100, 100, "100,0"},
		{807062248, 9999757606912, "0,008"},
		{1, 1000000, "<0,001"},
	}
	for _, tc := range cases {
		if got := formatPercentageForReport(tc.used, tc.limit); got != tc.want {
			t.Errorf("formatPercentageForReport(%d,%d) = %q, want %q", tc.used, tc.limit, got, tc.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "0 min",
		30 * time.Minute:              "30 min",
		2 * time.Hour:                 "2 h",
		2*time.Hour + 15*time.Minute:  "2 h 15 min",
		24 * time.Hour:                "1 día",
		25 * time.Hour:                "1 día 1 h",
		48 * time.Hour:                "2 días",
		51*time.Hour + 20*time.Minute: "2 días 3 h",
	}
	for age, want := range cases {
		if got := formatAge(age); got != want {
			t.Errorf("formatAge(%s) = %q, want %q", age, got, want)
		}
	}
}

func TestBytesFromGiBSaturates(t *testing.T) {
	if got := bytesFromGiB(1e30); got != math.MaxInt64 {
		t.Errorf("bytesFromGiB(1e30) = %d, want MaxInt64", got)
	}
	if got := bytesFromGiB(-1); got != 0 {
		t.Errorf("bytesFromGiB(-1) = %d, want 0", got)
	}
	if got := bytesFromGiB(1); got != 1<<30 {
		t.Errorf("bytesFromGiB(1) = %d, want %d", got, 1<<30)
	}
}
