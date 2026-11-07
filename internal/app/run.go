package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/accumulate"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/alerts"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/outbox"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/telegram"
)

// Exit codes of the command. The run command resolves several errors with the
// priority config (2) > persistence (4) > OCI (3) > Telegram (5). The
// concurrency code (6) is returned alone because the lock is acquired before
// any state is read.
const (
	ExitOK          = 0
	ExitConfig      = 2
	ExitOCI         = 3
	ExitPersistence = 4
	ExitTelegram    = 5
	ExitConcurrent  = 6
)

// DefaultDeadline is the global budget of one run, below the systemd
// TimeoutStartSec of 5 minutes. The last PersistenceMargin of the budget is
// reserved so a slow Telegram call cannot prevent saving the state.
const (
	DefaultDeadline   = 4 * time.Minute
	PersistenceMargin = 10 * time.Second
	// DeliveryTimeout bounds one Telegram message. It is always reduced to the
	// remaining global deadline.
	DeliveryTimeout = 45 * time.Second
)

// Logger is the small logging surface the run command uses. *logging.Logger
// implements it.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

type discardLogger struct{}

func (discardLogger) Debugf(string, ...any) {}
func (discardLogger) Infof(string, ...any)  {}
func (discardLogger) Warnf(string, ...any)  {}
func (discardLogger) Errorf(string, ...any) {}

// RunOptions controls one run. The zero value is a real run at time.Now().
type RunOptions struct {
	// Now is the reference instant; zero means time.Now().
	Now time.Time
	// DryRun reads and calculates but never writes, queues, sends or marks.
	DryRun bool
	// Sender delivers Telegram messages. It is required when telegram.enabled
	// is true and the run is not a dry run.
	Sender telegram.Sender
	// Logger receives the human-readable trace. Nil discards it.
	Logger Logger
	// Deadline is the global internal budget; zero means DefaultDeadline.
	Deadline time.Duration
}

// Delivered describes one message Telegram accepted.
type Delivered struct {
	Kind      string
	Period    string
	Level     int
	CreatedAt time.Time
}

// RunReport is the outcome of one run. ExitCode is the value the command must
// return; the categorized errors keep the priority rule visible in tests.
type RunReport struct {
	Now              time.Time
	DryRun           bool
	ExitCode         int
	NetworkEnabled   bool
	TelegramEnabled  bool
	Collect          CollectReport
	Sent             []Delivered
	Enqueued         int
	Collapsed        int
	Expired          int
	Dropped          int
	Skipped          int
	WouldSend        int
	Pending          int
	ConfigError      error
	PersistenceError error
	OCIError         error
	TelegramError    error
	ConcurrencyError error
	Warnings         []string
}

// ExitCodeFor returns the code of the report using the documented priority.
func (r RunReport) ExitCodeFor() int {
	switch {
	case r.ConfigError != nil:
		return ExitConfig
	case r.PersistenceError != nil:
		return ExitPersistence
	case r.OCIError != nil:
		return ExitOCI
	case r.TelegramError != nil:
		return ExitTelegram
	case r.ConcurrencyError != nil:
		return ExitConcurrent
	default:
		return ExitOK
	}
}

// Run executes the 13 steps of the cycle:
//
//  1. validate the configuration;
//  2. receive the logger and the OCI identity resolved by the caller;
//  3. acquire the exclusive non-blocking lock;
//  4. read the state and the histories through collectInternal;
//  5. plan the requery window;
//  6. query OCI without aborting on a single VNIC failure;
//  7. persist the histories;
//  8. sum the month;
//  9. drain the pending queue before evaluating new alerts;
//  10. evaluate monthly thresholds, the six-hour anomaly and the collection
//     health;
//  11. enqueue and send the new notifications, persisting the state after each
//     successful delivery;
//  12. persist the final state, counters and alert bookkeeping;
//  13. log the summary and release the lock (deferred).
//
// A dry run performs the reads and calculations but writes nothing, queues
// nothing, sends nothing and marks nothing. A network disabled configuration
// still takes the lock and returns 0 without querying OCI or writing state.
func Run(ctx context.Context, cfg config.Config, client oci.Client, store *persistence.Store, sender telegram.Sender, options RunOptions) RunReport {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	logger := options.Logger
	if logger == nil {
		logger = discardLogger{}
	}

	report := RunReport{
		Now:             now,
		DryRun:          options.DryRun,
		NetworkEnabled:  cfg.Network.Enabled,
		TelegramEnabled: cfg.Telegram.Enabled,
	}

	if err := cfg.Validate(); err != nil {
		report.ConfigError = err
		logger.Errorf("configuración inválida: %v", err)
		return finish(report, logger)
	}

	if store == nil {
		report.PersistenceError = errors.New("state store is nil")
		logger.Errorf("recopilación: %v", report.PersistenceError)
		return finish(report, logger)
	}

	deadline := options.Deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	workBudget := deadline - PersistenceMargin
	if workBudget <= 0 {
		workBudget = deadline
	}
	workCtx, cancel := context.WithTimeout(ctx, workBudget)
	defer cancel()

	// Step 3: the lock is acquired before any state is read, including for a
	// run with the network disabled.
	lock, err := store.Lock()
	if err != nil {
		if errors.Is(err, persistence.ErrLocked) {
			report.ConcurrencyError = err
			logger.Errorf("ejecución concurrente: %v", err)
		} else {
			report.PersistenceError = err
			logger.Errorf("no se pudo bloquear el directorio de estado: %v", err)
		}
		return finish(report, logger)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			logger.Warnf("no se pudo liberar el lock: %v", err)
		}
	}()

	if !cfg.Network.Enabled {
		// Contract: run exits 0 without querying OCI. The pending queue is not
		// processed either; its 7 day expiry is applied the next time the run
		// actually collects or the monitoring is re-enabled.
		logger.Infof("network.enabled=false: no se consulta OCI; la cola de pendientes espera a que se reactive")
		return finish(report, logger)
	}
	if client == nil {
		report.OCIError = errors.New("oci client is nil")
		logger.Errorf("OCI: %v", report.OCIError)
		return finish(report, logger)
	}

	// Steps 4 to 8: read the state and the histories, query OCI, normalize and
	// sum the month. The state is kept in memory: Run persists it in step 7
	// (histories) and after every successful delivery.
	collected, err := collectInternal(workCtx, cfg, client, store, CollectOptions{
		Now:          now,
		DryRun:       options.DryRun,
		Logf:         logger.Debugf,
		QueryTimeout: queryTimeout(workCtx),
	})
	report.Collect = collected.report
	report.Warnings = append(report.Warnings, collected.report.Warnings...)
	if err != nil {
		report.PersistenceError = err
		logger.Errorf("recopilación: %v", err)
		return finish(report, logger)
	}
	if len(collected.report.Failures) > 0 {
		report.OCIError = summarizeQueryFailures(collected.report.Failures)
		for _, failure := range collected.report.Failures {
			logger.Errorf("OCI: vnic %s %s %s a %s: %v",
				failure.Job.VNICID, failure.Job.Direction,
				formatTimestamp(failure.Job.Start), formatTimestamp(failure.Job.End), failure.Err)
		}
	}
	for _, id := range collected.report.NeverVisible {
		logger.Warnf("vnic %s: nunca fue visible en las consultas exitosas; sus horas quedan incomplete y cuenta como fallo de recopilación", id)
	}
	for _, id := range collected.report.Warnings {
		logger.Warnf("vnic %s: dejó de aparecer en métricas; se conserva last_visible y no se marcan buckets incomplete", id)
	}
	for month, stats := range collected.report.LateCorrections {
		logger.Warnf("mes %s: %d buckets corregidos tras el cierre; se descartan (%+d bytes)", month, stats.Buckets, stats.DeltaBytes)
	}
	if len(collected.report.Finalized) > 0 {
		logger.Infof("meses finalizados en esta ejecución: %v", collected.report.Finalized)
	}

	state := collected.state
	histories := collected.histories

	if !options.DryRun {
		// Step 7: the history is persisted before any notification is sent.
		if err := saveCollectedHistories(store, collected); err != nil {
			report.PersistenceError = err
			logger.Errorf("no se pudo persistir el historial: %v", err)
			return finish(report, logger)
		}
	}

	currentMonth := persistence.FormatMonth(now)
	previousMonth, err := persistence.PreviousMonth(currentMonth)
	if err != nil {
		previousMonth = ""
	}
	loadOptions := persistence.LoadOptions{Quarantine: !options.DryRun}
	for _, month := range []string{currentMonth, previousMonth} {
		if month == "" {
			continue
		}
		if _, ok := histories[month]; ok {
			continue
		}
		history, _, err := store.LoadHistoryWith(month, loadOptions)
		if err != nil {
			report.PersistenceError = err
			logger.Errorf("no se pudo leer el historial de %s: %v", month, err)
			return finish(report, logger)
		}
		histories[month] = history
	}

	// Step 9: the queue is expired and drained before the monthly and anomaly
	// alerts are evaluated. The collection health is planned first (see below)
	// so a recovered outage removes its queued error instead of delivering it.
	pending, expired := outbox.Expire(state.Pending, now)
	state.Pending = pending
	report.Expired = len(expired)
	if len(expired) > 0 {
		logger.Warnf("cola: %d notificaciones caducadas (más de 7 días) descartadas", len(expired))
	}

	telegramEnabled := cfg.Telegram.Enabled
	if telegramEnabled && state.TelegramEnabled != nil && !*state.TelegramEnabled {
		// Transition from disabled to enabled: the stale monthly alerts are
		// collapsed and the evaluators propose a single one at the current
		// level. Anomaly and collection notifications keep their own semantics.
		kept, dropped := outbox.DropKind(state.Pending, persistence.NotificationMonthlyThreshold)
		state.Pending = kept
		report.Collapsed += len(dropped)
		logger.Infof("telegram habilitado de nuevo: %d alertas mensuales pendientes colapsadas; se evaluará una sola con el nivel actual", len(dropped))
	}
	if !options.DryRun {
		state.TelegramEnabled = &telegramEnabled
	}

	if telegramEnabled && !options.DryRun && sender == nil {
		report.ConfigError = errors.New("telegram.enabled=true pero faltan TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID")
		logger.Errorf("Telegram: %v", report.ConfigError)
		if err := store.SaveState(state); err != nil {
			report.PersistenceError = err
			logger.Errorf("no se pudo persistir el estado: %v", err)
		}
		return finish(report, logger)
	}

	// The collection health is planned before the drain: when the collection
	// has recovered, the queued collection_failure must be removed and
	// replaced by a single recovery instead of being delivered as a stale
	// error. The proposed recovery itself is enqueued and sent in step 11.
	newAlerts := planCollectionHealth(&state, cfg, histories, now, logger)

	sendBlocked := false
	if telegramEnabled && !options.DryRun {
		var drain drainResult
		state, drain, err = drainPending(workCtx, cfg, store, sender, state, histories, now, logger)
		if err != nil {
			report.PersistenceError = err
			logger.Errorf("no se pudo persistir el estado tras un envío: %v", err)
			return finish(report, logger)
		}
		report.Sent = append(report.Sent, drain.Sent...)
		if drain.Failed {
			report.TelegramError = drain.Err
			sendBlocked = true
			logger.Errorf("telegram: no se pudo enviar un pendiente; se conserva en la cola: %v", drain.Err)
		}
	}

	// Step 10: evaluate the monthly thresholds and the six-hour anomaly with
	// the state already drained. The evaluators never send or write.
	input := alerts.Input{Now: now, Config: cfg, State: state, Histories: histories}
	for _, alert := range alerts.Evaluate(input).Alerts {
		newAlerts = append(newAlerts, pendingAlert{
			Notification: alert.Notification(),
			Text:         RenderAlert(cfg, alert, now),
		})
	}

	if options.DryRun {
		// The pending queue is what the real run would drain; the new alerts
		// are simulated below without touching the in-memory queue.
		report.Pending = len(state.Pending)
		if telegramEnabled {
			report.WouldSend = len(state.Pending)
		}
	}

	for _, candidate := range newAlerts {
		if !telegramEnabled {
			report.Skipped++
			logger.Infof("alerta %s del período %s: no encolada ni enviada porque telegram.enabled=false", candidate.Notification.Kind, candidate.Notification.Period)
			continue
		}
		if options.DryRun {
			simulated, enqueueResult := outbox.Enqueue(state.Pending, candidate.Notification, now)
			_ = simulated
			if enqueueResult.Added {
				report.WouldSend++
				logger.Infof("simulación: se habría encolado y enviado %s del período %s (nivel %d)",
					candidate.Notification.Kind, candidate.Notification.Period, candidate.Notification.Level)
			} else {
				logger.Debugf("simulación: %s del período %s ya estaría pendiente con un nivel igual o superior; no se enviaría",
					candidate.Notification.Kind, candidate.Notification.Period)
			}
			if enqueueResult.Collapsed {
				report.Collapsed++
			}
			report.Dropped += len(enqueueResult.DroppedOverflow)
			continue
		}

		// Step 11: enqueue before sending so a crash between the two leaves a
		// retryable pending notification.
		updated, enqueueResult := outbox.Enqueue(state.Pending, candidate.Notification, now)
		state.Pending = updated
		if enqueueResult.Added {
			report.Enqueued++
			if err := store.SaveState(state); err != nil {
				report.PersistenceError = err
				logger.Errorf("no se pudo persistir el estado tras encolar: %v", err)
				return finish(report, logger)
			}
		}
		if enqueueResult.Collapsed {
			report.Collapsed++
		}
		for _, dropped := range enqueueResult.DroppedOverflow {
			report.Dropped++
			logger.Warnf("cola llena: se descarta la notificación más antigua (%s del período %s)", dropped.Kind, dropped.Period)
		}
		if !enqueueResult.Added {
			logger.Debugf("alerta %s del período %s ya estaba pendiente con un nivel igual o superior; no se reenvía",
				candidate.Notification.Kind, candidate.Notification.Period)
			continue
		}
		if sendBlocked {
			// A previous delivery failed; keep this one queued for the next
			// run instead of hammering Telegram.
			continue
		}

		text := AddPreviousPeriodNotice(candidate.Text, candidate.Notification, now)
		sendCtx, cancelDelivery := deliveryContext(workCtx)
		err := sender.SendMessage(sendCtx, text)
		cancelDelivery()
		if err != nil {
			var retryErr *telegram.RetryAfterError
			if errors.As(err, &retryErr) {
				logger.Warnf("telegram: límite de velocidad con retry_after %s; la notificación queda en cola y la ejecución termina", retryErr.RetryAfter)
			}
			report.TelegramError = err
			sendBlocked = true
			state.Pending = bumpAttempt(state.Pending, candidate.Notification, now)
			logger.Errorf("telegram: %s del período %s: %v", candidate.Notification.Kind, candidate.Notification.Period, err)
			if saveErr := store.SaveState(state); saveErr != nil {
				report.PersistenceError = saveErr
				logger.Errorf("no se pudo persistir el estado tras un fallo de Telegram: %v", saveErr)
				return finish(report, logger)
			}
			continue
		}

		state.Pending = outbox.Remove(state.Pending, candidate.Notification)
		markDelivered(&state, candidate.Notification, cfg, now)
		report.Sent = append(report.Sent, deliveredFrom(candidate.Notification))
		if err := store.SaveState(state); err != nil {
			report.PersistenceError = err
			logger.Errorf("mensaje entregado pero no se pudo persistir el estado; un reintento puede duplicarlo: %v", err)
			return finish(report, logger)
		}
	}

	if !options.DryRun {
		report.Pending = len(state.Pending)
	}

	// Step 12: the final state carries last_run, last_successful_query, the
	// failure counters and the alert bookkeeping. When a message was sent this
	// is an extra write; it also covers the run that sends nothing.
	if !options.DryRun {
		if err := store.SaveState(state); err != nil {
			report.PersistenceError = err
			logger.Errorf("no se pudo persistir el estado final: %v", err)
			return finish(report, logger)
		}
	}

	return finish(report, logger)
}

type pendingAlert struct {
	Notification persistence.Notification
	Text         string
}

type drainResult struct {
	Sent   []Delivered
	Failed bool
	Err    error
}

// drainPending implements step 9 for an enabled Telegram: it sends the queued
// notifications in creation order and persists the state after each delivery.
// The first failure stops the drain and leaves the rest queued.
func drainPending(ctx context.Context, cfg config.Config, store *persistence.Store, sender telegram.Sender, state persistence.State, histories map[string]*persistence.History, now time.Time, logger Logger) (persistence.State, drainResult, error) {
	result := drainResult{}
	pending := outbox.Sort(state.Pending)
	for _, notification := range pending {
		summary := summaryFor(cfg, histories, notification.Period)
		text := RenderPending(cfg, notification, summary, now)
		text = AddPreviousPeriodNotice(text, notification, now)

		sendCtx, cancel := deliveryContext(ctx)
		err := sender.SendMessage(sendCtx, text)
		cancel()
		if err != nil {
			state.Pending = bumpAttempt(state.Pending, notification, now)
			if saveErr := store.SaveState(state); saveErr != nil {
				return state, result, saveErr
			}
			result.Failed = true
			result.Err = err
			return state, result, nil
		}

		state.Pending = outbox.Remove(state.Pending, notification)
		markDelivered(&state, notification, cfg, now)
		result.Sent = append(result.Sent, deliveredFrom(notification))
		if err := store.SaveState(state); err != nil {
			return state, result, err
		}
	}
	return state, result, nil
}

// planCollectionHealth applies the persistent collection error and recovery
// rules and returns the notifications they propose. It mutates the in-memory
// state (failure range and queue) but never sends or persists.
func planCollectionHealth(state *persistence.State, cfg config.Config, histories map[string]*persistence.History, now time.Time, logger Logger) []pendingAlert {
	threshold := cfg.Network.CollectionFailureThreshold
	if threshold < 1 {
		threshold = 3
	}
	cooldown := time.Duration(cfg.Network.CollectionFailureCooldownHours) * time.Hour
	if cooldown <= 0 {
		cooldown = 24 * time.Hour
	}

	failures := state.ConsecutiveFailures
	if failures > 0 {
		if state.Alerts.CollectionFailure == nil {
			state.Alerts.CollectionFailure = &persistence.CollectionFailureState{}
		}
		failureState := state.Alerts.CollectionFailure
		if failureState.FailureSince == nil {
			since := now
			if state.LastFailure != nil && !state.LastFailure.After(now) {
				since = state.LastFailure.UTC()
			}
			failureState.FailureSince = &since
		}
		if failures < threshold {
			logger.Debugf("fallos consecutivos de OCI: %d de %d antes de alertar", failures, threshold)
			return nil
		}
		if failureState.LastNotifiedAt != nil && now.Before(failureState.LastNotifiedAt.Add(cooldown)) {
			logger.Debugf("error persistente de recopilación en enfriamiento hasta %s", formatTimestamp(failureState.LastNotifiedAt.Add(cooldown)))
			return nil
		}
		if hasPendingKind(state.Pending, persistence.NotificationCollectionFailure) {
			logger.Debugf("ya hay una alerta de error persistente en la cola; no se duplica")
			return nil
		}
		since := *failureState.FailureSince
		last := since
		if state.LastFailure != nil && state.LastFailure.After(since) {
			last = state.LastFailure.UTC()
		}
		period := persistence.FormatMonth(now)
		text := RenderCollectionFailure(cfg, summaryFor(cfg, histories, period), now, since, last, failures, threshold)
		return []pendingAlert{{
			Notification: persistence.Notification{
				Kind:      persistence.NotificationCollectionFailure,
				Period:    period,
				CreatedAt: now,
				Text:      text,
			},
			Text: text,
		}}
	}

	if state.Alerts.CollectionFailure == nil || state.Alerts.CollectionFailure.FailureSince == nil {
		return nil
	}
	failureState := state.Alerts.CollectionFailure
	since := failureState.FailureSince.UTC()
	last := since
	if state.LastFailure != nil && state.LastFailure.After(since) {
		last = state.LastFailure.UTC()
	}
	queued, hadQueued := takePendingKind(state, persistence.NotificationCollectionFailure)
	sentBefore := failureState.LastNotifiedAt != nil && !failureState.LastNotifiedAt.Before(since)

	period := persistence.FormatMonth(since)
	text := RenderCollectionRecovery(cfg, summaryFor(cfg, histories, period), now, since, last)
	failureState.FailureSince = nil

	if sentBefore || hadQueued {
		if hadQueued && queued.Period != "" {
			// Keep the period of the failure that was queued, which is the one
			// the operator saw.
			period = queued.Period
			text = RenderCollectionRecovery(cfg, summaryFor(cfg, histories, period), now, since, last)
		}
		return []pendingAlert{{
			Notification: persistence.Notification{
				Kind:      persistence.NotificationCollectionRecovery,
				Period:    period,
				CreatedAt: now,
				Text:      text,
			},
			Text: text,
		}}
	}

	logger.Infof("recopilación recuperada tras fallos entre %s y %s; no se había generado una alerta de error, solo se registra",
		formatTimestamp(since), formatTimestamp(last))
	return nil
}

func markDelivered(state *persistence.State, notification persistence.Notification, cfg config.Config, now time.Time) {
	switch notification.Kind {
	case persistence.NotificationMonthlyThreshold, persistence.NotificationAnomaly6H:
		alerts.MarkNotificationNotified(state, notification, cfg.Network.AlertPercentages, now)
	case persistence.NotificationCollectionFailure:
		if state.Alerts.CollectionFailure == nil {
			state.Alerts.CollectionFailure = &persistence.CollectionFailureState{}
		}
		at := now.UTC()
		state.Alerts.CollectionFailure.LastNotifiedAt = &at
	}
}

func bumpAttempt(pending []persistence.Notification, target persistence.Notification, now time.Time) []persistence.Notification {
	out := outbox.Sort(pending)
	for i := range out {
		if !outbox.Same(out[i], target) {
			continue
		}
		out[i].Attempts++
		at := now.UTC()
		out[i].LastAttempt = &at
	}
	return out
}

func deliveredFrom(notification persistence.Notification) Delivered {
	return Delivered{
		Kind:      notification.Kind,
		Period:    notification.Period,
		Level:     notification.Level,
		CreatedAt: notification.CreatedAt,
	}
}

func hasPendingKind(pending []persistence.Notification, kind string) bool {
	for _, notification := range pending {
		if notification.Kind == kind {
			return true
		}
	}
	return false
}

// takePendingKind removes the first pending notification of kind and returns
// it. It reports false when there was none.
func takePendingKind(state *persistence.State, kind string) (persistence.Notification, bool) {
	for i, notification := range state.Pending {
		if notification.Kind != kind {
			continue
		}
		state.Pending = append(state.Pending[:i:i], state.Pending[i+1:]...)
		return notification, true
	}
	return persistence.Notification{}, false
}

func summaryFor(cfg config.Config, histories map[string]*persistence.History, month string) accumulate.Summary {
	if month != "" {
		if history, ok := histories[month]; ok && history != nil {
			return accumulate.Summarize(history, optionsFromConfig(cfg))
		}
	}
	return accumulate.Summary{Month: month}
}

func summarizeQueryFailures(failures []QueryFailure) error {
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%d consultas a OCI fallaron; primer error: %w", len(failures), failures[0].Err)
}

func queryTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return DefaultQueryTimeout
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Second
	}
	if remaining < DefaultQueryTimeout {
		return remaining
	}
	return DefaultQueryTimeout
}

func deliveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := DeliveryTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			timeout = time.Second
		} else if remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func finish(report RunReport, logger Logger) RunReport {
	report.ExitCode = report.ExitCodeFor()
	if report.DryRun {
		logger.Infof("resumen (simulación): consultas=%d fallos=%d pendientes=%d envíos_simulados=%d descartadas=%d código=%d",
			report.Collect.Queries, len(report.Collect.Failures), report.Pending, report.WouldSend, report.Expired+report.Dropped, report.ExitCode)
		return report
	}
	logger.Infof("resumen: consultas=%d fallos_oci=%d enviadas=%d encoladas=%d pendientes=%d caducadas=%d descartadas=%d código=%d",
		report.Collect.Queries, len(report.Collect.Failures), len(report.Sent), report.Enqueued,
		report.Pending, report.Expired, report.Dropped, report.ExitCode)
	return report
}
