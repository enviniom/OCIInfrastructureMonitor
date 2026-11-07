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

// RealClient implements Client with the official OCI SDK and Instance Principals.
type RealClient struct {
	client monitoring.MonitoringClient
}

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

	resp, err := c.client.SummarizeMetricsData(ctx, monitoring.SummarizeMetricsDataRequest{
		CompartmentId:               common.String(query.CompartmentID),
		SummarizeMetricsDataDetails: details,
	})
	if err != nil {
		return nil, fmt.Errorf("summarize metrics: %w", err)
	}

	series := make([]Series, 0, len(resp.Items))
	for _, item := range resp.Items {
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
	req := monitoring.ListMetricsRequest{
		CompartmentId: common.String(compartmentID),
		ListMetricsDetails: monitoring.ListMetricsDetails{
			Namespace:        common.String(namespace),
			DimensionFilters: dimensionFilters,
		},
	}

	var definitions []MetricDefinition
	for {
		resp, err := c.client.ListMetrics(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("list metrics: %w", err)
		}
		for _, item := range resp.Items {
			definitions = append(definitions, MetricDefinition{
				Name:       deref(item.Name),
				Namespace:  deref(item.Namespace),
				Dimensions: item.Dimensions,
			})
		}
		if resp.OpcNextPage == nil || *resp.OpcNextPage == "" {
			break
		}
		req.Page = resp.OpcNextPage
	}
	return definitions, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
