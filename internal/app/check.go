package app

import (
	"context"
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
)

// CheckReport is the result of a read-only diagnostic run.
type CheckReport struct {
	Region        string
	CompartmentID string
	InstanceOCID  string
	VNICs         []VNICCheck
}

// VNICCheck is the diagnostic status of one configured VNIC.
type VNICCheck struct {
	OCID              string
	Visible           bool
	MetricDefinitions int
	Queried           bool
	DataPoints        int
}

// Check validates the configuration against OCI without modifying state or configuration.
func Check(ctx context.Context, cfg config.Config, client oci.Client, now time.Time) (CheckReport, error) {
	report := CheckReport{
		Region:        cfg.OCI.Region,
		CompartmentID: cfg.Network.CompartmentOCID,
		InstanceOCID:  cfg.Network.InstanceOCID,
		VNICs:         make([]VNICCheck, 0, len(cfg.Network.VNICOCIDs)),
	}

	// Check the last complete hour. The end time is exclusive.
	end := now.UTC().Truncate(time.Hour)
	start := end.Add(-time.Hour)

	for _, vnic := range cfg.Network.VNICOCIDs {
		definitions, err := client.ListMetrics(ctx, cfg.Network.CompartmentOCID, oci.Namespace, map[string]string{
			"resourceId": vnic,
		})
		if err != nil {
			return CheckReport{}, fmt.Errorf("vnic %s: %w", vnic, err)
		}

		status := VNICCheck{
			OCID:              vnic,
			Visible:           len(definitions) > 0,
			MetricDefinitions: len(definitions),
		}

		if status.Visible {
			query := oci.Query{
				CompartmentID: cfg.Network.CompartmentOCID,
				Namespace:     oci.Namespace,
				MQL:           fmt.Sprintf("VnicToNetworkBytes[1h]{resourceId = %q}.sum()", vnic),
				StartTime:     start,
				EndTime:       end,
				Resolution:    "1h",
			}
			series, err := client.Summarize(ctx, query)
			if err != nil {
				return CheckReport{}, fmt.Errorf("vnic %s: %w", vnic, err)
			}
			status.Queried = true
			for _, s := range series {
				status.DataPoints += len(s.Points)
			}
		}

		report.VNICs = append(report.VNICs, status)
	}

	return report, nil
}
