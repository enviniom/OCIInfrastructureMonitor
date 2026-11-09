package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/app"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/config"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/logging"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/telegram"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// The two factories are variables so the command tests can replace the OCI and
// Telegram transports without touching the production wiring.
var (
	newOCIClient = func(region string) (oci.Client, error) {
		return oci.New(region)
	}
	newTelegramSender = func(credentials telegram.Credentials) (telegram.Sender, error) {
		return telegram.New(credentials, telegram.Options{})
	}
)

func main() {
	os.Exit(execute(os.Args[1:]))
}

func execute(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "run":
		return executeRun(args[1:])
	case "check":
		return executeCheck(args[1:])
	case "status":
		return executeStatus(args[1:])
	case "report":
		return executeReport(args[1:])
	case "test-telegram":
		return executeTestTelegram(args[1:])
	case "version", "--version", "-version":
		if len(args) > 1 {
			fmt.Fprintf(os.Stderr, "%s: argumentos no admitidos: %v\n", args[0], args[1:])
			usage(os.Stderr)
			return 2
		}
		printVersion()
		return 0
	case "help", "--help", "-h":
		if len(args) > 1 {
			fmt.Fprintf(os.Stderr, "help: argumentos no admitidos: %v\n", args[1:])
			usage(os.Stderr)
			return 2
		}
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

// parseFlags parses the subcommand flags and rejects positional arguments.
// ErrHelp is not an error: flag already printed the help and the command exits
// with 0. Any other parse error, or a leftover positional argument, exits 2.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "%s: argumentos no admitidos: %v\n", fs.Name(), fs.Args())
		fs.Usage()
		return 2, false
	}
	return 0, true
}

func executeRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	dryRun := fs.Bool("dry-run", false, "simula la ejecución sin escribir estado, encolar ni enviar")
	telegramEnv := fs.String("telegram-env", telegram.DefaultEnvPath, "ruta del archivo con TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	logger, err := logging.NewFromName(cfg.Logging.Level, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	// The credentials are validated before resolving the OCI identity so a
	// missing token (configuration, 2) wins over an identity failure (3).
	var sender telegram.Sender
	if cfg.Network.Enabled && cfg.Telegram.Enabled && !*dryRun {
		credentials, err := telegram.LoadCredentials(*telegramEnv)
		if err != nil {
			logger.Errorf("configuración de Telegram: %v", err)
			return 2
		}
		sender, err = newTelegramSender(credentials)
		if err != nil {
			logger.Errorf("Telegram: %v", err)
			return 2
		}
	}

	var client oci.Client
	if cfg.Network.Enabled {
		client, err = newOCIClient(cfg.OCI.Region)
		if err != nil {
			logger.Errorf("OCI: no se pudo resolver la identidad con Instance Principals: %v", err)
			return 3
		}
	}

	store := persistence.New(cfg.Storage.StateDir)
	report := app.Run(context.Background(), cfg, client, store, sender, app.RunOptions{
		Now:    time.Now(),
		DryRun: *dryRun,
		Logger: logger,
	})
	writeRunReport(os.Stdout, os.Stderr, report)
	return report.ExitCode
}

func executeTestTelegram(args []string) int {
	fs := flag.NewFlagSet("test-telegram", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	telegramEnv := fs.String("telegram-env", telegram.DefaultEnvPath, "ruta del archivo con TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	credentials, err := telegram.LoadCredentials(*telegramEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}
	sender, err := newTelegramSender(credentials)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram: %v\n", err)
		return 2
	}

	now := time.Now().UTC()
	store := persistence.New(cfg.Storage.StateDir)
	// A diagnostic must not move a corrupt state file aside: read without
	// quarantine. Report the persistence problem but still test Telegram.
	report := app.BuildStatusWithOptions(store, cfg, now, persistence.LoadOptions{Quarantine: false})
	for _, err := range report.Errors {
		fmt.Fprintf(os.Stderr, "test-telegram: aviso de estado: %v\n", err)
	}
	text := app.RenderTestMessage(cfg, report.Months[0].Summary, now)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sender.SendMessage(ctx, text); err != nil {
		fmt.Fprintf(os.Stderr, "Telegram: no se pudo enviar el mensaje de prueba: %v\n", err)
		return 5
	}
	fmt.Println("test-telegram: mensaje de prueba enviado")
	return 0
}

// executeReport sends the daily digest of the current and previous months. It
// only reads state.json and the histories: it never queries OCI, never takes
// the lock and never writes state, so it cannot interfere with the run cycle.
// The figures are those of the last successful run; the message quotes that
// instant so staleness is visible.
func executeReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	telegramEnv := fs.String("telegram-env", telegram.DefaultEnvPath, "ruta del archivo con TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID")
	dryRun := fs.Bool("dry-run", false, "imprime el resumen en stdout sin enviarlo por Telegram")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	now := time.Now().UTC()
	store := persistence.New(cfg.Storage.StateDir)
	// Read-only, like status: a corrupt file is reported but never moved.
	report := app.BuildDailyReport(store, cfg, now)
	for _, err := range report.Status.Errors {
		fmt.Fprintf(os.Stderr, "report: aviso de estado: %v\n", err)
	}
	text := app.RenderDailySummary(cfg, report)

	if *dryRun {
		fmt.Println(text)
		return reportExitCode(report, 0)
	}

	if !cfg.Telegram.Enabled {
		fmt.Println("report: telegram.enabled=false; no se envía el resumen diario")
		return reportExitCode(report, 0)
	}

	credentials, err := telegram.LoadCredentials(*telegramEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	if !report.HasData {
		fmt.Println("report: no hay datos en el historial; no se envía el resumen diario")
		return reportExitCode(report, 0)
	}

	sender, err := newTelegramSender(credentials)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sender.SendMessage(ctx, text); err != nil {
		fmt.Fprintf(os.Stderr, "Telegram: no se pudo enviar el resumen diario: %v\n", err)
		return reportExitCode(report, 5)
	}
	fmt.Println("report: resumen diario enviado")
	return reportExitCode(report, 0)
}

// reportExitCode applies the documented priority: a persistence problem (4)
// wins over a Telegram failure (5), so a corrupt state is never hidden behind
// the outcome of the delivery.
func reportExitCode(report app.DailyReport, code int) int {
	if len(report.Status.Errors) > 0 {
		return 4
	}
	return code
}

func executeStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	store := persistence.New(cfg.Storage.StateDir)
	report := app.BuildStatus(store, cfg, time.Now())
	writeStatus(os.Stdout, report)

	if len(report.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "status: error de persistencia; revisa los archivos indicados")
		return 4
	}
	return 0
}

func executeCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	if !cfg.Network.Enabled {
		fmt.Println("check: network.enabled=false; no se consultó OCI")
		return 0
	}

	client, err := newOCIClient(cfg.OCI.Region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OCI error: %v\n", err)
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := app.Check(ctx, cfg, client, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "OCI error: %v\n", err)
		return 3
	}

	fmt.Println("check: OK")
	fmt.Printf("region: %s\n", report.Region)
	fmt.Printf("compartment: %s\n", report.CompartmentID)
	fmt.Printf("instance: %s\n", report.InstanceOCID)

	allVisible := writeCheckReport(os.Stdout, report)
	if !allVisible {
		fmt.Fprintln(os.Stderr, "warning: alguna VNIC no es visible en métricas; revisa el OCID, el compartimento o si ha tenido tráfico")
	}
	return 0
}

func writeCheckReport(w io.Writer, report app.CheckReport) bool {
	allVisible := true
	for _, vnic := range report.VNICs {
		if !vnic.Visible {
			allVisible = false
			fmt.Fprintf(w, "vnic %s: NO VISIBLE: puede no existir, no tener métricas o no haber tenido tráfico\n", vnic.OCID)
			continue
		}
		fmt.Fprintf(w, "vnic %s: visible (%d definiciones, %d datapoints en la última hora)\n",
			vnic.OCID, vnic.MetricDefinitions, vnic.DataPoints)
	}
	return allVisible
}

func writeRunReport(w, errW io.Writer, report app.RunReport) {
	label := "run"
	if report.DryRun {
		label = "run (simulación)"
	}
	pending := "n/d"
	if report.PendingKnown {
		pending = fmt.Sprint(report.Pending)
	}
	fmt.Fprintf(w, "%s: consultas_oci=%d fallos_oci=%d enviadas=%d encoladas=%d pendientes=%s caducadas=%d descartadas=%d",
		label, report.Collect.Queries, len(report.Collect.Failures), len(report.Sent),
		report.Enqueued, pending, report.Expired, report.Dropped)
	if report.DailyReportDue {
		fmt.Fprintf(w, " resumen_diario=%s", dailyReportOutcome(report))
	}
	fmt.Fprintf(w, " código=%d\n", report.ExitCode)
	if report.DryRun {
		fmt.Fprintf(w, "run (simulación): no se escribió estado, no se encoló nada, no se envió nada; alertas simuladas=%d\n", report.WouldSend)
	}
	if report.ConfigError != nil {
		fmt.Fprintf(errW, "configuración: %v\n", report.ConfigError)
	}
	if report.PersistenceError != nil {
		fmt.Fprintf(errW, "persistencia: %v\n", report.PersistenceError)
	}
	if report.OCIError != nil {
		fmt.Fprintf(errW, "OCI: %v\n", report.OCIError)
	}
	if report.TelegramError != nil {
		fmt.Fprintf(errW, "Telegram: %v\n", report.TelegramError)
	}
	if report.ConcurrencyError != nil {
		fmt.Fprintf(errW, "concurrencia: %v\n", report.ConcurrencyError)
	}
}

func dailyReportOutcome(report app.RunReport) string {
	switch {
	case report.DryRun:
		return "simulado"
	case report.DailyReportSent:
		return "enviado"
	case report.DailyReportError != nil:
		return "fallido"
	default:
		return "omitido"
	}
}

// buildVCS reads the VCS metadata the Go toolchain embeds when the binary is
// built from a git checkout (the default). It is empty when the build used
// -buildvcs=false or the source tree was not a repository.
func buildVCS() (revision, commitTime string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", false
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			commitTime = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	return revision, commitTime, modified
}

func printVersion() {
	revision, commitTime, modified := buildVCS()
	commitValue := commit
	if revision != "" {
		if commitValue == "unknown" {
			commitValue = shortRevision(revision)
		}
		if modified && !strings.HasSuffix(commitValue, "-dirty") {
			commitValue += "-dirty"
		}
	}
	fmt.Printf("oci-infrastructure-monitor %s\n", version)
	fmt.Printf("commit: %s\n", commitValue)
	fmt.Printf("built: %s\n", buildDate)
	if revision != "" {
		fmt.Printf("vcs.revision: %s\n", revision)
		if commitTime != "" {
			fmt.Printf("vcs.time: %s\n", commitTime)
		}
		fmt.Printf("vcs.modified: %t\n", modified)
	}
	fmt.Printf("go: %s\n", runtime.Version())
	fmt.Printf("os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `Uso:
  oci-infrastructure-monitor run [--dry-run] [--config PATH] [--telegram-env PATH]
  oci-infrastructure-monitor check [--config PATH]
  oci-infrastructure-monitor status [--config PATH]
  oci-infrastructure-monitor report [--dry-run] [--config PATH] [--telegram-env PATH]
  oci-infrastructure-monitor test-telegram [--config PATH] [--telegram-env PATH]
  oci-infrastructure-monitor version | --version
  oci-infrastructure-monitor --help

run: consulta OCI, actualiza el estado y envía las alertas y los pendientes por Telegram.
     Un fallo de OCI no aborta la ejecución. --dry-run no escribe estado, no encola y no envía.
     En la ejecución cuya hora local coincide con telegram.daily_report_hour (18 por defecto)
     envía además el resumen diario del mes en curso y del mes anterior.
check: valida configuración, autenticación y visibilidad de las VNIC en OCI Monitoring.
       No modifica estado ni configuración.
status: muestra el estado persistido sin consultar OCI ni escribir estado ni historial.
        Si encuentra un archivo corrupto lo informa y sale 4; no lo mueve.
report: envía por demanda el mismo resumen diario; sirve para probarlo o reenviarlo si el run
        de esa hora no ocurrió. Solo lee el estado y el historial: no consulta OCI ni escribe
        estado. --dry-run imprime el resumen en stdout sin enviarlo; sin ningún dato no envía nada.
test-telegram: envía un mensaje de prueba explícito. Funciona aunque telegram.enabled sea false.

El token y el Chat ID se leen de telegram.env (TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID) o del
entorno; nunca se aceptan por argumentos ni se escriben en log.
`)
}
