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

// DefaultQueryTimeout bounds one OCI Monitoring request. The run command sets a
// smaller value when its remaining deadline is shorter.
const DefaultQueryTimeout = 30 * time.Second

// CollectOptions controls one collection pass.
type CollectOptions struct {
	// Now is the reference instant; zero means time.Now().
	Now time.Time
	// DryRun reads and calculates but writes no state and no history.
	DryRun bool
	// Logf receives human-readable messages, such as the discarded
	// corrections of a finalized month. It may be nil.
	Logf func(format string, args ...any)
	// QueryTimeout bounds each individual OCI query. Zero means
	// DefaultQueryTimeout.
	QueryTimeout time.Duration
}

// QueryFailure is one failed OCI query. A failure never aborts the run.
type QueryFailure struct {
	Job accumulate.Job
	Err error
}

// CollectReport is the outcome of one collection pass. It carries data instead
// of logging so the caller decides the log level and the exit code.
type CollectReport struct {
	Now       time.Time
	DryRun    bool
	Queries   int
	Succeeded int
	Failures  []QueryFailure
	// NeverVisible lists the configured VNICs that have never produced a
	// datapoint in any successful query. A VNIC that was visible before and
	// stopped appearing is reported in Warnings instead and does not count as
	// a collection failure.
	NeverVisible    []string
	GapsFilled      int
	Finalized       []string
	Corrections     map[string]accumulate.CorrectionStats
	LateCorrections map[string]accumulate.CorrectionStats
	Summaries       map[string]accumulate.Summary
	Warnings        []string
}

// collectResult is the in-memory outcome the run command needs after the
// collection, before it decides when to persist state.json.
type collectResult struct {
	report       CollectReport
	state        persistence.State
	histories    map[string]*persistence.History
	months       []string
	finalizedNow map[string]bool
	options      accumulate.Options
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
	result, err := collectInternal(ctx, cfg, client, store, options)
	if err != nil {
		return result.report, err
	}
	if options.DryRun || !cfg.Network.Enabled {
		return result.report, nil
	}
	if err := saveCollected(store, result); err != nil {
		return result.report, err
	}
	return result.report, nil
}

// collectInternal performs the collection without persisting anything. It is
// shared by Collect and Run: the run command persists the histories in step 7
// and the state only after each successful delivery (or at the end when there
// was nothing to send).
func collectInternal(ctx context.Context, cfg config.Config, client oci.Client, store *persistence.Store, options CollectOptions) (collectResult, error) {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	result := collectResult{
		report: CollectReport{
			Now:             now,
			DryRun:          options.DryRun,
			Corrections:     map[string]accumulate.CorrectionStats{},
			LateCorrections: map[string]accumulate.CorrectionStats{},
			Summaries:       map[string]accumulate.Summary{},
		},
		finalizedNow: map[string]bool{},
	}
	result.state = persistence.NewState()

	if !cfg.Network.Enabled {
		// network.enabled=false exits 0 without querying OCI or touching state.
		return result, nil
	}
	if client == nil {
		return result, errors.New("oci client is nil")
	}
	if store == nil {
		return result, errors.New("state store is nil")
	}
	if !options.DryRun {
		if err := store.Ensure(); err != nil {
			return result, fmt.Errorf("prepare state directory: %w", err)
		}
	}

	// A dry run must not move corrupt files: it only reads and reports.
	loadOptions := persistence.LoadOptions{Quarantine: !options.DryRun}
	state, err := store.LoadStateWith(loadOptions)
	if err != nil {
		return result, err
	}
	result.state = state

	opts := optionsFromConfig(cfg)
	result.options = opts
	historyMonths, err := store.ListMonths()
	if err != nil {
		return result, err
	}
	months := accumulate.MonthsToConsider(now, state, opts, historyMonths)
	result.months = months
	histories := make(map[string]*persistence.History, len(months))
	for _, month := range months {
		history, _, err := store.LoadHistoryWith(month, loadOptions)
		if err != nil {
			return result, err
		}
		histories[month] = history
		if history.Finalized && !state.IsFinalized(month) {
			// The history file is the source of truth for the finalization.
			state.MarkFinalized(month)
		}
	}
	result.histories = histories

	plan := accumulate.PlanMonths(now, opts, state, histories)

	for _, monthPlan := range plan.Months {
		if len(monthPlan.Gaps) == 0 {
			continue
		}
		result.report.GapsFilled += accumulate.ApplyGaps(histories[monthPlan.Month], monthPlan.Gaps, now)
	}

	type outcome struct {
		job     accumulate.Job
		samples []accumulate.Sample
	}
	var outcomes []outcome
	failedMonths := map[string]bool{}
	observedVNICs := map[string]bool{}
	dataHours := map[string][]time.Time{}

	queryTimeout := options.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = DefaultQueryTimeout
	}

	for _, monthPlan := range plan.Months {
		for _, job := range monthPlan.Jobs {
			result.report.Queries++
			queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
			series, err := client.Summarize(queryCtx, oci.Query{
				CompartmentID: cfg.Network.CompartmentOCID,
				Namespace:     oci.Namespace,
				MQL:           job.MQL(),
				StartTime:     job.Start,
				EndTime:       job.End,
				Resolution:    "1h",
			})
			cancel()
			if err != nil {
				// A failing VNIC does not abort the run: its hours become
				// incomplete and the rest of the VNICs still run.
				result.report.Failures = append(result.report.Failures, QueryFailure{Job: job, Err: err})
				failedMonths[job.Month] = true
				accumulate.MarkJobFailed(histories[job.Month], job, now)
				continue
			}
			result.report.Succeeded++
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
			result.report.Warnings = append(result.report.Warnings, vnic)
		}
		if updated.FirstVisible == nil {
			// The VNIC has never produced a datapoint. The roadmap treats it
			// as a collection failure for the persistent-error alert because a
			// wrong OCID cannot be told apart from an idle VNIC without extra
			// network permissions.
			result.report.NeverVisible = append(result.report.NeverVisible, vnic)
		}
	}

	for _, outcome := range outcomes {
		month := outcome.job.Month
		reconciled := accumulate.Reconcile(histories[month], outcome.job, outcome.samples, now, opts, state.VNIC(outcome.job.VNICID))
		if reconciled.Corrections.Buckets > 0 {
			stats := result.report.Corrections[month]
			stats.Add(reconciled.Corrections)
			result.report.Corrections[month] = stats
			state.AddCorrections(month, reconciled.Corrections.Buckets, reconciled.Corrections.DeltaBytes, now)
		}
		if reconciled.LateCorrections.Buckets > 0 {
			stats := result.report.LateCorrections[month]
			stats.Add(reconciled.LateCorrections)
			result.report.LateCorrections[month] = stats
		}
	}

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
			result.report.Finalized = append(result.report.Finalized, monthPlan.Month)
			result.finalizedNow[monthPlan.Month] = true
		}
	}

	if result.report.Queries > 0 {
		if len(result.report.Failures) > 0 || len(result.report.NeverVisible) > 0 {
			state.ConsecutiveFailures++
			state.LastFailure = &now
		} else {
			state.ConsecutiveFailures = 0
		}
	}
	if result.report.Succeeded > 0 {
		state.LastSuccessfulQuery = &now
	}
	state.LastRun = &now
	result.state = state

	for _, month := range months {
		if !histories[month].HasData() {
			continue
		}
		result.report.Summaries[month] = accumulate.Summarize(histories[month], opts)
	}

	if options.Logf != nil {
		for month, stats := range result.report.LateCorrections {
			options.Logf("mes %s: %d buckets corregidos tras el cierre; se descartan (%+d bytes)", month, stats.Buckets, stats.DeltaBytes)
		}
	}

	return result, nil
}

// saveCollected writes the histories and then the state. Collect uses it; Run
// only reuses the history half because it persists the state after each
// successful delivery.
func saveCollected(store *persistence.Store, result collectResult) error {
	if err := saveCollectedHistories(store, result); err != nil {
		return err
	}
	if err := store.SaveState(result.state); err != nil {
		return err
	}
	return nil
}

// saveCollectedHistories performs step 7 of the run cycle: persist the history
// of every month that has data or was finalized in this pass. Finalized files
// that were already immutable on disk are not rewritten.
func saveCollectedHistories(store *persistence.Store, result collectResult) error {
	for _, month := range result.months {
		history := result.histories[month]
		if history == nil {
			continue
		}
		if history.Finalized && !result.finalizedNow[month] {
			// Already immutable on disk.
			continue
		}
		if !history.HasData() && !history.Finalized {
			continue
		}
		if err := store.SaveHistory(history); err != nil {
			return err
		}
	}
	return nil
}

// optionsFromConfig builds the accumulation options from the configuration.
// PublishZeros stays false until the pending empirical validation confirms
// that oci_vcn publishes explicit zero datapoints; the conservative reading is
// that OCI omits them, so a no_data streak does not invalidate the total.
func optionsFromConfig(cfg config.Config) accumulate.Options {
	return accumulate.OptionsFromConfig(cfg)
}
