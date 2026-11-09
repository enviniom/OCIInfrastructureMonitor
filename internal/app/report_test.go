package app

import (
	"strings"
	"testing"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func digestConfig() config.Config {
	cfg := testConfig()
	cfg.Network.MonthlyLimitGiB = 10
	return cfg
}

func digestStatus() StatusReport {
	finalizedAt := utcHour(2026, 11, 3, 0)
	lastQuery := utcHour(2026, 11, 8, 11)
	return StatusReport{
		State: persistence.State{
			SchemaVersion:       persistence.CurrentSchemaVersion,
			LastSuccessfulQuery: &lastQuery,
			ConsecutiveFailures: 2,
			Pending: []persistence.Notification{{
				Kind: persistence.NotificationAnomaly6H, Period: "2026-11", CreatedAt: lastQuery,
			}},
		},
		Months: []MonthStatus{
			{
				Month: "2026-11", Label: "actual", HasHistory: true,
				Summary: accumulate.Summary{
					Month: "2026-11", HasData: true, Status: persistence.StatusComplete,
					Outbound: accumulate.DirectionSummary{
						Direction: persistence.DirectionOutbound, Bytes: 1 << 30,
						Buckets: 168, Complete: 168, Status: persistence.StatusComplete,
						FirstHour: utcHour(2026, 11, 1, 0), LastHour: utcHour(2026, 11, 7, 23),
					},
				},
			},
			{
				Month: "2026-10", Label: "anterior", HasHistory: true,
				Finalized: true, FinalizedAt: &finalizedAt,
				Summary: accumulate.Summary{
					Month: "2026-10", HasData: true, Status: persistence.StatusComplete,
					Outbound: accumulate.DirectionSummary{
						Direction: persistence.DirectionOutbound, Bytes: 2 << 30,
						Buckets: 744, Complete: 744, Status: persistence.StatusComplete,
						FirstHour: utcHour(2026, 10, 1, 0), LastHour: utcHour(2026, 10, 31, 23),
					},
				},
			},
		},
	}
}

func TestRenderDailySummary(t *testing.T) {
	cfg := digestConfig()
	report := DailyReport{Now: utcHour(2026, 11, 8, 12), Status: digestStatus(), HasData: true}
	out := RenderDailySummary(cfg, report)

	wants := []string{
		"OCI Infrastructure Monitor — resumen diario",
		"fecha de evaluación: 2026-11-08 12:00:00 UTC",
		"última consulta exitosa: 2026-11-08 11:00:00 UTC",
		"medición: tráfico saliente de red (VnicToNetworkBytes, suma horaria)",
		"mes en curso 2026-11 (día 8 de 30)",
		"datos: 2026-11-01 00:00 → 2026-11-07 23:00 UTC (hace 12 h)",
		"consumo saliente: 1,00 GiB (1.073.741.824 bytes)",
		"límite mensual: 10,00 GiB — consumido 10,0 %",
		"media: 0,14 GiB/día · proyección al ritmo actual: ~4,29 GiB/mes",
		"estado del acumulado: completo",
		"buckets: 168 (168 complete, 0 provisional, 0 no_data, 0 incomplete)",
		"estado del mes: abierto",
		"mes anterior 2026-10",
		"datos: 2026-10-01 00:00 → 2026-10-31 23:00 UTC",
		"consumo saliente: 2,00 GiB (2.147.483.648 bytes)",
		"estado del mes: finalizado el 2026-11-03 00:00:00 UTC",
		"avisos:",
		"2 fallos consecutivos de recopilación",
		"notificaciones pendientes de envío: 1",
		"fuente: estado persistido por el último run; este resumen no consulta OCI.",
		notBillableNote,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The configured limit is only compared against the month in course: the
	// previous total has no historical limit of its own.
	if got := strings.Count(out, "límite mensual:"); got != 1 {
		t.Errorf("limit lines = %d, want 1:\n%s", got, out)
	}
}

func TestRenderDailySummaryTinyPercentage(t *testing.T) {
	cfg := digestConfig()
	cfg.Network.MonthlyLimitGiB = 9313
	status := digestStatus()
	status.Months[0].Summary.Outbound.Bytes = 807062248

	out := RenderDailySummary(cfg, DailyReport{Now: utcHour(2026, 11, 8, 12), Status: status})
	if !strings.Contains(out, "consumido 0,008 %") {
		t.Errorf("output missing the three-decimal percentage:\n%s", out)
	}
}

func TestRenderDailySummarySkipsPaceWithoutADay(t *testing.T) {
	cfg := digestConfig()
	status := digestStatus()
	status.Months[0].Summary.Outbound.FirstHour = utcHour(2026, 11, 8, 0)
	status.Months[0].Summary.Outbound.LastHour = utcHour(2026, 11, 8, 10)

	out := RenderDailySummary(cfg, DailyReport{Now: utcHour(2026, 11, 8, 12), Status: status})
	if !strings.Contains(out, "datos: 2026-11-08 00:00 → 2026-11-08 10:00 UTC (hace 1 h)") {
		t.Errorf("output missing the short coverage line:\n%s", out)
	}
	if strings.Contains(out, "media:") {
		t.Errorf("output has a daily rate from less than a day of data:\n%s", out)
	}
}

func TestRenderDailySummaryWithoutHistory(t *testing.T) {
	cfg := digestConfig()
	report := DailyReport{
		Now: utcHour(2026, 11, 8, 12),
		Status: StatusReport{Months: []MonthStatus{
			{Month: "2026-11", Label: "actual"},
			{Month: "2026-10", Label: "anterior"},
		}},
	}
	out := RenderDailySummary(cfg, report)

	for _, want := range []string{
		"mes en curso 2026-11",
		"mes anterior 2026-10",
		"sin historial",
		"última consulta exitosa: no disponible",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "consumo saliente") || strings.Contains(out, "límite mensual") {
		t.Errorf("output has consumption lines without data:\n%s", out)
	}
	if strings.Contains(out, "avisos:") {
		t.Errorf("output has warnings without a problem:\n%s", out)
	}
}

func TestRenderDailySummaryProvisionalAndGrace(t *testing.T) {
	cfg := digestConfig()
	status := digestStatus()
	status.Months[1].Finalized = false
	status.Months[1].FinalizedAt = nil
	status.Months[1].Grace = true
	status.Months[1].Summary.Outbound.Status = persistence.StatusProvisional
	status.Months[1].Summary.Outbound.Reason = "3 buckets provisionales"
	status.Months[1].Summary.Outbound.Provisional = 3

	out := RenderDailySummary(cfg, DailyReport{Now: utcHour(2026, 11, 8, 12), Status: status})
	for _, want := range []string{
		"estado del acumulado: provisional (3 buckets provisionales)",
		"estado del mes: no finalizado (en gracia)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDailySummaryWarnsAboutCorruptFiles(t *testing.T) {
	cfg := digestConfig()
	status := digestStatus()
	status.StateErr = persistence.ErrCorrupt
	status.Months[1].Err = persistence.ErrCorrupt

	out := RenderDailySummary(cfg, DailyReport{Now: utcHour(2026, 11, 8, 12), Status: status})
	for _, want := range []string{
		"AVISO: no se pudo leer state.json",
		"AVISO: no se pudo leer el historial de 2026-10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDailySummaryInboundOnlyMonth(t *testing.T) {
	cfg := digestConfig()
	status := digestStatus()
	status.Months[0].Summary = accumulate.Summary{
		Month: "2026-11", HasData: true, Status: persistence.StatusComplete,
		Inbound: accumulate.DirectionSummary{
			Direction: persistence.DirectionInbound, Bytes: 1 << 20,
			Buckets: 1, Complete: 1, Status: persistence.StatusComplete,
		},
	}

	out := RenderDailySummary(cfg, DailyReport{Now: utcHour(2026, 11, 8, 12), Status: status})
	if !strings.Contains(out, "consumo saliente: sin datos registrados") {
		t.Errorf("inbound-only month presented as outbound data:\n%s", out)
	}
}

func TestBuildDailyReport(t *testing.T) {
	cfg := statusConfig(t)
	store := persistence.New(cfg.Storage.StateDir)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}

	empty := BuildDailyReport(store, cfg, utcHour(2026, 11, 7, 13))
	if empty.HasData {
		t.Error("HasData = true without histories")
	}
	if len(empty.Status.Months) != 2 {
		t.Fatalf("months = %d, want 2", len(empty.Status.Months))
	}

	history := persistence.NewHistory("2026-11")
	history.Set(persistence.Bucket{
		ResourceID: cfg.Network.VNICOCIDs[0], Direction: persistence.DirectionOutbound,
		Hour: utcHour(2026, 11, 1, 0), Bytes: 100, Status: persistence.StatusComplete,
		CapturedAt: utcHour(2026, 11, 7, 13),
	})
	if err := store.SaveHistory(history); err != nil {
		t.Fatal(err)
	}

	report := BuildDailyReport(store, cfg, utcHour(2026, 11, 7, 13))
	if !report.HasData {
		t.Error("HasData = false with a current-month bucket")
	}
	if store.HasState() {
		t.Error("BuildDailyReport wrote state.json")
	}
}
