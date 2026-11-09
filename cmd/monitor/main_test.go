package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/app"
)

func TestWriteCheckReportVisible(t *testing.T) {
	var buf bytes.Buffer
	allVisible := writeCheckReport(&buf, app.CheckReport{VNICs: []app.VNICCheck{
		{OCID: "ocid1.vnic.oc1.iad.example", Visible: true, MetricDefinitions: 2, DataPoints: 3},
	}})
	if !allVisible {
		t.Fatal("allVisible = false, want true")
	}
	if !strings.Contains(buf.String(), "visible (2 definiciones, 3 datapoints") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestWriteCheckReportNotVisible(t *testing.T) {
	var buf bytes.Buffer
	allVisible := writeCheckReport(&buf, app.CheckReport{VNICs: []app.VNICCheck{
		{OCID: "ocid1.vnic.oc1.iad.example", Visible: false},
	}})
	if allVisible {
		t.Fatal("allVisible = true, want false")
	}
	want := "NO VISIBLE: puede no existir, no tener métricas o no haber tenido tráfico"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("output = %q, want message %q", buf.String(), want)
	}
}

func TestWriteRunReportDailyReportOutcome(t *testing.T) {
	cases := []struct {
		name string
		in   app.RunReport
		want string
	}{
		{
			name: "sent",
			in:   app.RunReport{DailyReportDue: true, DailyReportSent: true},
			want: "resumen_diario=enviado",
		},
		{
			name: "simulated",
			in:   app.RunReport{DailyReportDue: true, DryRun: true},
			want: "resumen_diario=simulado",
		},
		{
			name: "failed",
			in:   app.RunReport{DailyReportDue: true, DailyReportError: errStub},
			want: "resumen_diario=fallido",
		},
		{
			name: "skipped",
			in:   app.RunReport{DailyReportDue: true},
			want: "resumen_diario=omitido",
		},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		writeRunReport(&buf, &buf, tc.in)
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("%s: output = %q, want %q", tc.name, buf.String(), tc.want)
		}
		if !strings.Contains(buf.String(), "código=0") {
			t.Errorf("%s: output = %q, want the exit code", tc.name, buf.String())
		}
	}

	var buf bytes.Buffer
	writeRunReport(&buf, &buf, app.RunReport{})
	if strings.Contains(buf.String(), "resumen_diario") {
		t.Errorf("output = %q, must not mention the daily report when it is not due", buf.String())
	}
}

var errStub = errors.New("stub")
