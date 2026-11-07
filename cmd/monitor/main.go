package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
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
	case "test-telegram":
		return executeTestTelegram(args[1:])
	case "version":
		printVersion()
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

func executeRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	dryRun := fs.Bool("dry-run", false, "simula la ejecución sin escribir estado, encolar ni enviar")
	telegramEnv := fs.String("telegram-env", telegram.DefaultEnvPath, "ruta del archivo con TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID")
	if err := fs.Parse(args); err != nil {
		return 2
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
	if err := fs.Parse(args); err != nil {
		return 2
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
	summary := app.BuildStatus(store, cfg, now).Months[0].Summary
	text := app.RenderTestMessage(cfg, summary, now)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sender.SendMessage(ctx, text); err != nil {
		fmt.Fprintf(os.Stderr, "Telegram: no se pudo enviar el mensaje de prueba: %v\n", err)
		return 5
	}
	fmt.Println("test-telegram: mensaje de prueba enviado")
	return 0
}

func executeStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", config.DefaultPath, "ruta del archivo de configuración")
	if err := fs.Parse(args); err != nil {
		return 2
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
	if err := fs.Parse(args); err != nil {
		return 2
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
	fmt.Fprintf(w, "%s: consultas_oci=%d fallos_oci=%d enviadas=%d encoladas=%d pendientes=%d caducadas=%d descartadas=%d código=%d\n",
		label, report.Collect.Queries, len(report.Collect.Failures), len(report.Sent),
		report.Enqueued, report.Pending, report.Expired, report.Dropped, report.ExitCode)
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

func printVersion() {
	fmt.Printf("oci-infrastructure-monitor %s\n", version)
	fmt.Printf("commit: %s\n", commit)
	fmt.Printf("built: %s\n", buildDate)
	fmt.Printf("go: %s\n", runtime.Version())
	fmt.Printf("os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `Uso:
  oci-infrastructure-monitor run [--dry-run] [--config PATH] [--telegram-env PATH]
  oci-infrastructure-monitor check [--config PATH]
  oci-infrastructure-monitor status [--config PATH]
  oci-infrastructure-monitor test-telegram [--config PATH] [--telegram-env PATH]
  oci-infrastructure-monitor version
  oci-infrastructure-monitor --help

run: consulta OCI, actualiza el estado y envía las alertas y los pendientes por Telegram.
     Un fallo de OCI no aborta la ejecución. --dry-run no escribe estado, no encola y no envía.
check: valida configuración, autenticación y visibilidad de las VNIC en OCI Monitoring.
       No modifica estado ni configuración.
status: muestra el estado persistido sin consultar OCI ni escribir estado ni historial.
        Si encuentra un archivo corrupto lo mueve a *.corrupt-* y sale 4.
test-telegram: envía un mensaje de prueba explícito. Funciona aunque telegram.enabled sea false.

El token y el Chat ID se leen de telegram.env (TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID) o del
entorno; nunca se aceptan por argumentos ni se escriben en log.
`)
}
