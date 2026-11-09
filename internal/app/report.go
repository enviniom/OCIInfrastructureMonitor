package app

import (
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// DailyReport is the read-only view of the current and previous months used by
// report and by run when it sends the daily report. It is built from state.json
// and the monthly histories: it never queries OCI, takes the lock or writes
// state. In the report command the figures are those of the last successful
// run; in run they are the data just collected.
type DailyReport struct {
	Now    time.Time
	Status StatusReport
	// HasData is true when at least one of the two months has buckets. A
	// report without any data is not sent: a daily "sin datos" would be noise
	// and the collection error alert already covers a broken monitor.
	HasData bool
}

// BuildDailyReport reads the state and the histories of the current and
// previous months with the same read-only rules as status: no lock and no
// quarantine, so a corrupt file is reported but never moved.
func BuildDailyReport(store *persistence.Store, cfg config.Config, now time.Time) DailyReport {
	now = now.UTC()
	status := BuildStatus(store, cfg, now)
	report := DailyReport{Now: now, Status: status}
	for _, month := range status.Months {
		if month.HasHistory && month.Summary.HasData {
			report.HasData = true
			break
		}
	}
	return report
}
