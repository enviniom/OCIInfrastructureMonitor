package oci

import (
	"context"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/monitoring"
)

type fakeServiceError struct {
	status int
	code   string
}

func (e fakeServiceError) Error() string          { return "fake service error" }
func (e fakeServiceError) GetHTTPStatusCode() int { return e.status }
func (e fakeServiceError) GetMessage() string     { return "fake service error" }
func (e fakeServiceError) GetCode() string        { return e.code }
func (e fakeServiceError) GetOpcRequestID() string {
	return "request-id"
}

type fakeMonitoringAPI struct {
	summarize      func(context.Context, monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error)
	list           func(context.Context, monitoring.ListMetricsRequest) (monitoring.ListMetricsResponse, error)
	summarizeCalls int
	listCalls      int
}

func (f *fakeMonitoringAPI) SummarizeMetricsData(ctx context.Context, request monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error) {
	f.summarizeCalls++
	return f.summarize(ctx, request)
}

func (f *fakeMonitoringAPI) ListMetrics(ctx context.Context, request monitoring.ListMetricsRequest) (monitoring.ListMetricsResponse, error) {
	f.listCalls++
	return f.list(ctx, request)
}

func TestSummarizeRetriesTransientError(t *testing.T) {
	old := retryInitialBackoff
	retryInitialBackoff = time.Millisecond
	defer func() { retryInitialBackoff = old }()

	value := 5.0
	fake := &fakeMonitoringAPI{}
	fake.summarize = func(context.Context, monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error) {
		if fake.summarizeCalls == 1 {
			return monitoring.SummarizeMetricsDataResponse{}, fakeServiceError{status: 503, code: "ServiceUnavailable"}
		}
		return monitoring.SummarizeMetricsDataResponse{Items: []monitoring.MetricData{{
			Dimensions: map[string]string{"resourceId": "vnic-a"},
			AggregatedDatapoints: []monitoring.AggregatedDatapoint{{
				Timestamp: &common.SDKTime{Time: time.Now()},
				Value:     &value,
			}},
		}}}, nil
	}

	client := &RealClient{client: fake}
	series, err := client.Summarize(context.Background(), Query{CompartmentID: "c", Namespace: Namespace, MQL: "m", Resolution: "1h"})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if fake.summarizeCalls != 2 {
		t.Errorf("calls = %d, want 2", fake.summarizeCalls)
	}
	if len(series) != 1 || series[0].ResourceID != "vnic-a" || len(series[0].Points) != 1 || series[0].Points[0].Value != 5 {
		t.Errorf("series = %+v", series)
	}
}

func TestSummarizeDoesNotRetryPermanentError(t *testing.T) {
	fake := &fakeMonitoringAPI{
		summarize: func(context.Context, monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error) {
			return monitoring.SummarizeMetricsDataResponse{}, fakeServiceError{status: 400, code: "InvalidParameter"}
		},
	}
	client := &RealClient{client: fake}
	if _, err := client.Summarize(context.Background(), Query{}); err == nil {
		t.Fatal("Summarize() error = nil")
	}
	if fake.summarizeCalls != 1 {
		t.Errorf("calls = %d, want 1", fake.summarizeCalls)
	}
}

func TestSummarizeSkipsNilPoints(t *testing.T) {
	fake := &fakeMonitoringAPI{
		summarize: func(context.Context, monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error) {
			return monitoring.SummarizeMetricsDataResponse{Items: []monitoring.MetricData{{
				Dimensions:           map[string]string{"resourceId": "vnic-a"},
				AggregatedDatapoints: []monitoring.AggregatedDatapoint{{}},
			}}}, nil
		},
	}
	client := &RealClient{client: fake}
	series, err := client.Summarize(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if len(series) != 1 || len(series[0].Points) != 0 {
		t.Errorf("series = %+v, want one stream without points", series)
	}
}

func TestListMetricsPaginates(t *testing.T) {
	page2 := "page-2"
	fake := &fakeMonitoringAPI{
		list: func(_ context.Context, request monitoring.ListMetricsRequest) (monitoring.ListMetricsResponse, error) {
			if request.Page == nil {
				return monitoring.ListMetricsResponse{
					Items:       []monitoring.Metric{{Name: common.String("A"), Namespace: common.String(Namespace)}},
					OpcNextPage: &page2,
				}, nil
			}
			return monitoring.ListMetricsResponse{
				Items: []monitoring.Metric{{Name: common.String("B"), Namespace: common.String(Namespace)}},
			}, nil
		},
	}
	client := &RealClient{client: fake}
	definitions, err := client.ListMetrics(context.Background(), "c", Namespace, nil)
	if err != nil {
		t.Fatalf("ListMetrics() error = %v", err)
	}
	if fake.listCalls != 2 {
		t.Errorf("calls = %d, want 2", fake.listCalls)
	}
	if len(definitions) != 2 || definitions[0].Name != "A" || definitions[1].Name != "B" {
		t.Errorf("definitions = %+v", definitions)
	}
}
