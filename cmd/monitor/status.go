package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/app"
)

const gib = 1 << 30

func writeStatus(w io.Writer, report app.StatusReport) {
	fmt.Fprintln(w, "oci-infrastructure-monitor — estado")
	writeStateHeader(w, report)
	fmt.Fprintln(w)

	for _, month := range report.Months {
		writeMonthStatus(w, month)
		fmt.Fprintln(w)
	}

	if len(report.Warnings) > 0 {
		fmt.Fprintln(w, "avisos:")
		for _, warning := range report.Warnings {
			fmt.Fprintf(w, "  %s\n", warning)
		}
		fmt.Fprintln(w)
	}

	if len(report.Errors) > 0 {
		fmt.Fprintln(w, "errores:")
		for _, err := range report.Errors {
			fmt.Fprintf(w, "  %v\n", err)
		}
	}
}

func writeStateHeader(w io.Writer, report app.StatusReport) {
	fmt.Fprintf(w, "directorio: %s\n", report.StateDir)
	if report.StateErr != nil {
		fmt.Fprintf(w, "esquema: ILEGIBLE (%v)\n", report.StateErr)
		return
	}
	fmt.Fprintf(w, "esquema: %d\n", report.State.SchemaVersion)
	if !report.StateExists {
		fmt.Fprintln(w, "estado: sin estado registrado (todavía no se ejecutó run)")
	}
	fmt.Fprintf(w, "última ejecución: %s\n", formatTimePtr(report.State.LastRun))
	fmt.Fprintf(w, "última consulta exitosa: %s\n", formatTimePtr(report.State.LastSuccessfulQuery))
	fmt.Fprintf(w, "fallos consecutivos: %d\n", report.State.ConsecutiveFailures)
	fmt.Fprintf(w, "notificaciones pendientes: %d\n", len(report.State.Pending))
}

func writeMonthStatus(w io.Writer, month app.MonthStatus) {
	fmt.Fprintf(w, "mes %s: %s (%s)\n", month.Label, month.Month, app.MonthStateLabel(month))
	if month.Err != nil {
		fmt.Fprintf(w, "  error: %v\n", month.Err)
		return
	}
	if !month.HasHistory {
		fmt.Fprintln(w, "  sin historial")
		return
	}

	outbound := month.Summary.Outbound
	fmt.Fprintf(w, "  saliente: %s\n", formatBytes(outbound.Bytes))
	fmt.Fprintf(w, "  acumulado: %s\n", app.AccumulatedLabel(month.Summary))
	fmt.Fprintf(w, "  buckets: %d (complete %d, provisional %d, no_data %d, incomplete %d)\n",
		outbound.Buckets, outbound.Complete, outbound.Provisional, outbound.NoData, outbound.Incomplete)

	if inbound := month.Summary.Inbound; inbound.Buckets > 0 {
		fmt.Fprintf(w, "  entrante: %s (informativo, no se suma al saliente)\n", formatBytes(inbound.Bytes))
	}

	if corrections := month.Corrections; corrections.BucketsCorrected > 0 || corrections.DeltaBytes != 0 {
		fmt.Fprintf(w, "  correcciones durante la gracia: %d buckets, %s bytes\n",
			corrections.BucketsCorrected, formatSigned(corrections.DeltaBytes))
	}
}

func formatBytes(bytes int64) string {
	gibValue := float64(bytes) / float64(gib)
	value := strconv.FormatFloat(gibValue, 'f', 2, 64)
	value = strings.Replace(value, ".", ",", 1)
	return fmt.Sprintf("%s GiB (%s bytes)", value, formatThousands(bytes))
}

// formatThousands groups the digits of value with dots, as used in es-ES.
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

func formatSigned(value int64) string {
	if value > 0 {
		return "+" + formatThousands(value)
	}
	return formatThousands(value)
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "nunca"
	}
	return formatTime(*t)
}
