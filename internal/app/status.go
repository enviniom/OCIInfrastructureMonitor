package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// VNICWarning is a VNIC that stopped producing datapoints.
type VNICWarning struct {
	OCID        string
	Since       time.Time
	LastVisible *time.Time
}

// MonthStatus is the persisted view of one month. It never queries OCI.
type MonthStatus struct {
	Month string
	Label string
	Err   error
	// Finalized is true when the month is closed and immutable.
	Finalized   bool
	FinalizedAt *time.Time
	// Grace is true for the previous month while requery_window is still open.
	Grace bool
	// PendingFinalization is true for a closed month past its grace period
	// that has not been finalized yet.
	PendingFinalization bool
	HasHistory          bool
	Summary             accumulate.Summary
	Corrections         persistence.MonthCorrections
}

// StatusReport is the result of the read-only status command.
type StatusReport struct {
	StateDir    string
	State       persistence.State
	StateErr    error
	StateExists bool
	Months      []MonthStatus
	Warnings    []VNICWarning
	Errors      []error
}

// BuildStatus reads state.json and the history of the current and previous
// months without taking the lock and without querying OCI. A corrupt file only
// affects its own file: the rest of the status is still reported. It is a
// read-only diagnostic: it never moves a corrupt file. Only run, which holds
// the lock, applies the quarantine.
func BuildStatus(store *persistence.Store, cfg config.Config, now time.Time) StatusReport {
	return BuildStatusWithOptions(store, cfg, now, persistence.LoadOptions{Quarantine: false})
}

// BuildStatusWithOptions is BuildStatus with explicit load options. The
// test-telegram diagnostic uses Quarantine:false so a corrupt file is reported
// but never moved.
func BuildStatusWithOptions(store *persistence.Store, cfg config.Config, now time.Time, options persistence.LoadOptions) StatusReport {
	now = now.UTC()
	report := StatusReport{
		StateDir:    store.Dir(),
		State:       persistence.NewState(),
		StateExists: store.HasState(),
	}

	state, err := store.LoadStateWith(options)
	if err != nil {
		report.StateErr = err
		report.Errors = append(report.Errors, err)
	} else {
		report.State = state
	}

	requeryWindow := time.Duration(cfg.Network.RequeryWindowHours) * time.Hour
	if requeryWindow <= 0 {
		requeryWindow = 48 * time.Hour
	}

	current := persistence.FormatMonth(now)
	previous, err := persistence.PreviousMonth(current)
	if err != nil {
		previous = ""
	}

	for _, entry := range []struct {
		month   string
		label   string
		current bool
	}{
		{month: current, label: "actual", current: true},
		{month: previous, label: "anterior"},
	} {
		if entry.month == "" {
			continue
		}
		status := MonthStatus{
			Month:   entry.month,
			Label:   entry.label,
			Summary: accumulate.Summary{Month: entry.month},
		}
		history, exists, err := store.LoadHistoryWith(entry.month, options)
		if err != nil {
			status.Err = err
			report.Errors = append(report.Errors, err)
			report.Months = append(report.Months, status)
			continue
		}
		status.HasHistory = exists
		status.Summary = accumulate.Summarize(history, optionsFromConfig(cfg))
		status.Finalized = history.Finalized
		status.FinalizedAt = history.FinalizedAt
		status.Corrections = report.State.Corrections(entry.month)

		if !history.Finalized && !entry.current && exists {
			monthEnd, err := persistence.MonthEnd(entry.month)
			if err == nil {
				status.Grace = now.Before(monthEnd.Add(requeryWindow))
				status.PendingFinalization = !status.Grace
			}
		}
		report.Months = append(report.Months, status)
	}

	for id, vnic := range report.State.VNICs {
		if vnic.WarningSince == nil {
			continue
		}
		warning := VNICWarning{OCID: id, Since: vnic.WarningSince.UTC()}
		if vnic.LastVisible != nil {
			at := vnic.LastVisible.UTC()
			warning.LastVisible = &at
		}
		report.Warnings = append(report.Warnings, warning)
	}
	sort.Slice(report.Warnings, func(i, j int) bool {
		return report.Warnings[i].OCID < report.Warnings[j].OCID
	})
	return report
}

// String renders a warning for logs.
func (w VNICWarning) String() string {
	if w.LastVisible == nil {
		return fmt.Sprintf("%s: dejó de aparecer en métricas", w.OCID)
	}
	return fmt.Sprintf("%s: dejó de aparecer en métricas; último dato %s", w.OCID, w.LastVisible.Format(time.RFC3339))
}
