package main

import (
	"bytes"
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
