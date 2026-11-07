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
	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
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
	case "check":
		return executeCheck(args[1:])
	case "status":
		return executeStatus(args[1:])
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

	client, err := oci.New(cfg.OCI.Region)
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

func printVersion() {
	fmt.Printf("oci-infrastructure-monitor %s\n", version)
	fmt.Printf("commit: %s\n", commit)
	fmt.Printf("built: %s\n", buildDate)
	fmt.Printf("go: %s\n", runtime.Version())
	fmt.Printf("os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `Uso:
  oci-infrastructure-monitor check [--config PATH]
  oci-infrastructure-monitor status [--config PATH]
  oci-infrastructure-monitor version
  oci-infrastructure-monitor --help

check: valida configuración, autenticación y visibilidad de las VNIC en OCI Monitoring.
       No modifica estado ni configuración.
status: muestra el estado persistido sin consultar OCI ni escribir estado ni historial.
        Si encuentra un archivo corrupto lo mueve a *.corrupt-* y sale 4.
`)
}
