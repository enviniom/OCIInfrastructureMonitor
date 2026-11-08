package oci

import (
	"context"
	"fmt"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/common/auth"
	"github.com/oracle/oci-go-sdk/v65/monitoring"
)

// Namespace is the OCI Monitoring namespace for VNIC metrics.
const Namespace = "oci_vcn"

// Query describes one aggregated metric query.
type Query struct {
	CompartmentID string
	Namespace     string
	MQL           string
	StartTime     time.Time
	EndTime       time.Time
	Resolution    string
}

// Point is one aggregated datapoint.
type Point struct {
	Time  time.Time
	Value float64
}

// Series is one metric stream returned by a query.
type Series struct {
	ResourceID string
	Points     []Point
}

// MetricDefinition is a metric definition returned by ListMetrics.
type MetricDefinition struct {
	Name       string
	Namespace  string
	Dimensions map[string]string
}

// Client is the subset of OCI Monitoring used by the application.
type Client interface {
	Summarize(ctx context.Context, query Query) ([]Series, error)
	ListMetrics(ctx context.Context, compartmentID, namespace string, dimensionFilters map[string]string) ([]MetricDefinition, error)
}

// monitoringAPI is the subset of monitoring.MonitoringClient used here. The
// interface keeps RealClient testable without a live OCI endpoint.
type monitoringAPI interface {
	SummarizeMetricsData(context.Context, monitoring.SummarizeMetricsDataRequest) (monitoring.SummarizeMetricsDataResponse, error)
	ListMetrics(context.Context, monitoring.ListMetricsRequest) (monitoring.ListMetricsResponse, error)
}

// RealClient implements Client with the official OCI SDK and Instance Principals.
type RealClient struct {
	client monitoringAPI
}

// Retry policy for the Monitoring calls. A short retry absorbs a transient
// 429/5xx without hiding a permanent error; the caller's query timeout bounds
// the total.
const (
	retryMaxAttempts = 3
	retryMaxBackoff  = time.Second
)

// retryInitialBackoff is a variable so tests can avoid real waits.
var retryInitialBackoff = 250 * time.Millisecond

// New creates a Monitoring client authenticated with Instance Principals.
func New(region string) (*RealClient, error) {
	provider, err := auth.InstancePrincipalConfigurationProvider()
	if err != nil {
		return nil, fmt.Errorf("create instance principal provider: %w", err)
	}

	client, err := monitoring.NewMonitoringClientWithConfigurationProvider(provider)
	if err != nil {
		return nil, fmt.Errorf("create monitoring client: %w", err)
	}
	client.SetRegion(region)

	return &RealClient{client: client}, nil
}

// Summarize runs an aggregated MQL query.
func (c *RealClient) Summarize(ctx context.Context, query Query) ([]Series, error) {
	details := monitoring.SummarizeMetricsDataDetails{
		Namespace: common.String(query.Namespace),
		Query:     common.String(query.MQL),
		StartTime: &common.SDKTime{Time: query.StartTime},
		EndTime:   &common.SDKTime{Time: query.EndTime},
	}
	if query.Resolution != "" {
		details.Resolution = common.String(query.Resolution)
	}

	request := monitoring.SummarizeMetricsDataRequest{
		CompartmentId:               common.String(query.CompartmentID),
		SummarizeMetricsDataDetails: details,
	}
	var response monitoring.SummarizeMetricsDataResponse
	err := retry(ctx, func() error {
		var callErr error
		response, callErr = c.client.SummarizeMetricsData(ctx, request)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("summarize metrics: %w", err)
	}

	series := make([]Series, 0, len(response.Items))
	for _, item := range response.Items {
		s := Series{
			ResourceID: item.Dimensions["resourceId"],
			Points:     make([]Point, 0, len(item.AggregatedDatapoints)),
		}
		for _, dp := range item.AggregatedDatapoints {
			if dp.Timestamp == nil || dp.Value == nil {
				continue
			}
			s.Points = append(s.Points, Point{Time: dp.Timestamp.Time, Value: *dp.Value})
		}
		series = append(series, s)
	}
	return series, nil
}

// ListMetrics lists metric definitions, following pagination.
func (c *RealClient) ListMetrics(ctx context.Context, compartmentID, namespace string, dimensionFilters map[string]string) ([]MetricDefinition, error) {
	request := monitoring.ListMetricsRequest{
		CompartmentId: common.String(compartmentID),
		ListMetricsDetails: monitoring.ListMetricsDetails{
			Namespace:        common.String(namespace),
			DimensionFilters: dimensionFilters,
		},
	}

	var definitions []MetricDefinition
	for {
		var response monitoring.ListMetricsResponse
		err := retry(ctx, func() error {
			var callErr error
			response, callErr = c.client.ListMetrics(ctx, request)
			return callErr
		})
		if err != nil {
			return nil, fmt.Errorf("list metrics: %w", err)
		}
		for _, item := range response.Items {
			definitions = append(definitions, MetricDefinition{
				Name:       deref(item.Name),
				Namespace:  deref(item.Namespace),
				Dimensions: item.Dimensions,
			})
		}
		if response.OpcNextPage == nil || *response.OpcNextPage == "" {
			break
		}
		request.Page = response.OpcNextPage
	}
	return definitions, nil
}

// retry calls call until it succeeds, fails permanently or the retry budget is
// exhausted. Only errors that the OCI SDK classifies as retryable are retried.
func retry(ctx context.Context, call func() error) error {
	wait := retryInitialBackoff
	var err error
	for attempt := 1; attempt <= retryMaxAttempts; attempt++ {
		if err = call(); err == nil {
			return nil
		}
		if attempt == retryMaxAttempts || !common.IsErrorRetryableByDefault(err) {
			return err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		wait *= 2
		if wait > retryMaxBackoff {
			wait = retryMaxBackoff
		}
	}
	return err
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
