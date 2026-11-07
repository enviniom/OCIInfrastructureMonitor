package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
)

type mockClient struct {
	definitions map[string][]oci.MetricDefinition
	listErr     error
	series      []oci.Series
	sumErr      error
	listCalls   []string
	queries     []oci.Query
}

func (m *mockClient) ListMetrics(_ context.Context, _, _ string, dimensionFilters map[string]string) ([]oci.MetricDefinition, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	vnic := dimensionFilters["resourceId"]
	m.listCalls = append(m.listCalls, vnic)
	return m.definitions[vnic], nil
}

func (m *mockClient) Summarize(_ context.Context, query oci.Query) ([]oci.Series, error) {
	if m.sumErr != nil {
		return nil, m.sumErr
	}
	m.queries = append(m.queries, query)
	return m.series, nil
}

func testOCID(kind, fill string) string {
	return "ocid1." + kind + ".oc1.iad." + strings.Repeat(fill, 64)
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.OCI.Region = "us-ashburn-1"
	cfg.Network.CompartmentOCID = testOCID("compartment", "c")
	cfg.Network.InstanceOCID = testOCID("instance", "a")
	cfg.Network.VNICOCIDs = []string{
		testOCID("vnic", "b"),
		testOCID("vnic", "d"),
	}
	return cfg
}

func TestCheckVisible(t *testing.T) {
	cfg := testConfig()
	client := &mockClient{
		definitions: map[string][]oci.MetricDefinition{
			cfg.Network.VNICOCIDs[0]: {{Name: "VnicToNetworkBytes"}},
			cfg.Network.VNICOCIDs[1]: {{Name: "VnicFromNetworkBytes"}},
		},
		series: []oci.Series{{ResourceID: cfg.Network.VNICOCIDs[0], Points: []oci.Point{
			{Time: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), Value: 1},
			{Time: time.Date(2026, 11, 7, 12, 30, 0, 0, time.UTC), Value: 2},
		}}},
	}
	now := time.Date(2026, 11, 7, 13, 5, 0, 0, time.UTC)

	report, err := Check(context.Background(), cfg, client, now)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(report.VNICs) != 2 {
		t.Fatalf("VNICs = %d, want 2", len(report.VNICs))
	}
	for i, v := range report.VNICs {
		if !v.Visible {
			t.Errorf("VNIC[%d].Visible = false, want true", i)
		}
		if !v.Queried {
			t.Errorf("VNIC[%d].Queried = false, want true", i)
		}
		if v.DataPoints != 2 {
			t.Errorf("VNIC[%d].DataPoints = %d, want 2", i, v.DataPoints)
		}
	}
	if len(client.queries) != 2 {
		t.Fatalf("queries = %d, want 2", len(client.queries))
	}
	wantStart := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
	if !client.queries[0].StartTime.Equal(wantStart) || !client.queries[0].EndTime.Equal(wantEnd) {
		t.Errorf("query window = %v..%v, want %v..%v", client.queries[0].StartTime, client.queries[0].EndTime, wantStart, wantEnd)
	}
	if !strings.Contains(client.queries[0].MQL, cfg.Network.VNICOCIDs[0]) {
		t.Errorf("MQL %q does not contain the VNIC OCID", client.queries[0].MQL)
	}
}

func TestCheckNotVisible(t *testing.T) {
	cfg := testConfig()
	client := &mockClient{definitions: map[string][]oci.MetricDefinition{}}

	report, err := Check(context.Background(), cfg, client, time.Now())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for i, v := range report.VNICs {
		if v.Visible {
			t.Errorf("VNIC[%d].Visible = true, want false", i)
		}
		if v.Queried {
			t.Errorf("VNIC[%d].Queried = true, want false", i)
		}
	}
	if len(client.queries) != 0 {
		t.Fatalf("queries = %d, want 0", len(client.queries))
	}
}

func TestCheckListError(t *testing.T) {
	client := &mockClient{listErr: errors.New("denied")}
	if _, err := Check(context.Background(), testConfig(), client, time.Now()); err == nil {
		t.Fatal("Check() error = nil, want error")
	}
}

func TestCheckSummarizeError(t *testing.T) {
	cfg := testConfig()
	client := &mockClient{
		definitions: map[string][]oci.MetricDefinition{
			cfg.Network.VNICOCIDs[0]: {{Name: "VnicToNetworkBytes"}},
		},
		sumErr: errors.New("query failed"),
	}
	if _, err := Check(context.Background(), cfg, client, time.Now()); err == nil {
		t.Fatal("Check() error = nil, want error")
	}
}

func TestCheckNoVNICs(t *testing.T) {
	cfg := testConfig()
	cfg.Network.VNICOCIDs = nil
	report, err := Check(context.Background(), cfg, &mockClient{}, time.Now())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(report.VNICs) != 0 {
		t.Fatalf("VNICs = %d, want 0", len(report.VNICs))
	}
}
