package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// CollectOptions controls one collection pass.
type CollectOptions struct {
	// Now is the reference instant; zero means time.Now().
	Now time.Time
	// DryRun reads and calculates but writes no state and no history.
	DryRun bool
	// Logf receives human-readable messages, such as the discarded
	// corrections of a finalized month. It may be nil.
	Logf func(format string, args ...any)
}

// QueryFailure is one failed OCI query. A failure never aborts the run.
type QueryFailure struct {
	Job accumulate.Job
	Err error
}

// CollectReport is the outcome of one collection pass. It carries data instead
// of logging so the caller decides the log level and the exit code.
type CollectReport struct {
	Now             time.Time
	DryRun          bool
	Queries         int
	Succeeded       int
	Failures        []QueryFailure
	GapsFilled      int
	Finalized       []string
	Corrections     map[string]accumulate.CorrectionStats
	LateCorrections map[string]accumulate.CorrectionStats
	Summaries       map[string]accumulate.Summary
	Warnings        []string
}

// Collect implements steps 5 to 8 of the run cycle: plan the requery window,
// query OCI, normalize and overwrite the hourly buckets, sum the month and
// finalize the months whose grace period ended. It never sends notifications.
//
// The caller must hold the exclusive lock (Store.Lock) before calling Collect:
// the lock is acquired in step 3 of the run cycle, which belongs to the run
// command. OCI failures are returned in CollectReport.Failures, not as an
// error: a failing VNIC never aborts the pass. The error return is reserved for
// persistence problems.
func Collect(ctx context.Context, cfg config.Config, client oci.Client, store *persistence.Store, options CollectOptions) (CollectReport, error) {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	report := CollectReport{
		Now:             now,
		DryRun:          options.DryRun,
		Corrections:     map[string]accumulate.CorrectionStats{},
		LateCorrections: map[string]accumulate.CorrectionStats{},
		Summaries:       map[string]accumulate.Summary{},
	}

	if !cfg.Network.Enabled {
		// network.enabled=false exits 0 without querying OCI or touching state.
		return report, nil
	}
	if client == nil {
		return report, errors.New("oci client is nil")
	}
	if store == nil {
		return report, errors.New("state store is nil")
	}
	if !options.DryRun {
		if err := store.Ensure(); err != nil {
			return report, fmt.Errorf("prepare state directory: %w", err)
		}
	}

	// A dry run must not move corrupt files: it only reads and reports.
	loadOptions := persistence.LoadOptions{Quarantine: !options.DryRun}
	state, err := store.LoadStateWith(loadOptions)
	if err != nil {
		return report, err
	}

	opts := optionsFromConfig(cfg)
	historyMonths, err := store.ListMonths()
	if err != nil {
		return report, err
	}
	months := accumulate.MonthsToConsider(now, state, opts, historyMonths)
	histories := make(map[string]*persistence.History, len(months))
	for _, month := range months {
		history, _, err := store.LoadHistoryWith(month, loadOptions)
		if err != nil {
			return report, err
		}
		histories[month] = history
		if history.Finalized && !state.IsFinalized(month) {
			// The history file is the source of truth for the finalization.
			state.MarkFinalized(month)
		}
	}

	plan := accumulate.PlanMonths(now, opts, state, histories)

	for _, monthPlan := range plan.Months {
		if len(monthPlan.Gaps) == 0 {
			continue
		}
		report.GapsFilled += accumulate.ApplyGaps(histories[monthPlan.Month], monthPlan.Gaps, now)
	}

	type outcome struct {
		job     accumulate.Job
		samples []accumulate.Sample
	}
	var outcomes []outcome
	failedMonths := map[string]bool{}
	observedVNICs := map[string]bool{}
	dataHours := map[string][]time.Time{}

	for _, monthPlan := range plan.Months {
		for _, job := range monthPlan.Jobs {
			report.Queries++
			series, err := client.Summarize(ctx, oci.Query{
				CompartmentID: cfg.Network.CompartmentOCID,
				Namespace:     oci.Namespace,
				MQL:           job.MQL(),
				StartTime:     job.Start,
				EndTime:       job.End,
				Resolution:    "1h",
			})
			if err != nil {
				// A failing VNIC does not abort the run: its hours become
				// incomplete and the rest of the VNICs still run.
				report.Failures = append(report.Failures, QueryFailure{Job: job, Err: err})
				failedMonths[job.Month] = true
				accumulate.MarkJobFailed(histories[job.Month], job, now)
				continue
			}
			report.Succeeded++
			observedVNICs[job.VNICID] = true
			samples := accumulate.NormalizeSeries(series, job.Direction, job.Start, job.End)
			for _, sample := range samples {
				dataHours[sample.ResourceID] = append(dataHours[sample.ResourceID], sample.Hour)
			}
			outcomes = append(outcomes, outcome{job: job, samples: samples})
		}
	}

	// A VNIC is observed by a run only when at least one of its queries
	// succeeded. A failed query is not evidence that the VNIC disappeared.
	for _, vnic := range cfg.Network.VNICOCIDs {
		if !observedVNICs[vnic] {
			continue
		}
		updated, warned := accumulate.UpdateVNIC(state.VNIC(vnic), dataHours[vnic], now)
		state.SetVNIC(vnic, updated)
		if warned {
			report.Warnings = append(report.Warnings, vnic)
		}
	}

	for _, result := range outcomes {
		month := result.job.Month
		reconciled := accumulate.Reconcile(histories[month], result.job, result.samples, now, opts, state.VNIC(result.job.VNICID))
		if reconciled.Corrections.Buckets > 0 {
			stats := report.Corrections[month]
			stats.Add(reconciled.Corrections)
			report.Corrections[month] = stats
			state.AddCorrections(month, reconciled.Corrections.Buckets, reconciled.Corrections.DeltaBytes, now)
		}
		if reconciled.LateCorrections.Buckets > 0 {
			stats := report.LateCorrections[month]
			stats.Add(reconciled.LateCorrections)
			report.LateCorrections[month] = stats
		}
	}

	finalizedNow := map[string]bool{}
	for _, monthPlan := range plan.Months {
		if !monthPlan.Due || failedMonths[monthPlan.Month] {
			continue
		}
		history := histories[monthPlan.Month]
		if history.Finalized {
			continue
		}
		if history.Finalize(now) {
			state.MarkFinalized(monthPlan.Month)
			report.Finalized = append(report.Finalized, monthPlan.Month)
			finalizedNow[monthPlan.Month] = true
		}
	}

	if report.Queries > 0 {
		if len(report.Failures) > 0 {
			state.ConsecutiveFailures++
			state.LastFailure = &now
		} else {
			state.ConsecutiveFailures = 0
		}
	}
	if report.Succeeded > 0 {
		state.LastSuccessfulQuery = &now
	}
	state.LastRun = &now

	for _, month := range months {
		if !histories[month].HasData() {
			continue
		}
		report.Summaries[month] = accumulate.Summarize(histories[month], opts)
	}

	if options.Logf != nil {
		for month, stats := range report.LateCorrections {
			options.Logf("mes %s: %d buckets corregidos tras el cierre; se descartan (%+d bytes)", month, stats.Buckets, stats.DeltaBytes)
		}
	}

	if options.DryRun {
		return report, nil
	}

	for _, month := range months {
		history := histories[month]
		if history.Finalized && !finalizedNow[month] {
			// Already immutable on disk.
			continue
		}
		if !history.HasData() && !history.Finalized {
			continue
		}
		if err := store.SaveHistory(history); err != nil {
			return report, err
		}
	}
	if err := store.SaveState(state); err != nil {
		return report, err
	}
	return report, nil
}

// optionsFromConfig builds the accumulation options from the configuration.
// PublishZeros stays false until the pending empirical validation confirms
// that oci_vcn publishes explicit zero datapoints; the conservative reading is
// that OCI omits them, so a no_data streak does not invalidate the total.
func optionsFromConfig(cfg config.Config) accumulate.Options {
	return accumulate.OptionsFromConfig(cfg)
}
