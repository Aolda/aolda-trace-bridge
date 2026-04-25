package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/aolda/aolda-trace-bridge/internal/config"
	"github.com/aolda/aolda-trace-bridge/internal/exporter"
	"github.com/aolda/aolda-trace-bridge/internal/helper"
	"github.com/aolda/aolda-trace-bridge/internal/otlp"
	"github.com/aolda/aolda-trace-bridge/internal/redaction"
	"github.com/aolda/aolda-trace-bridge/internal/report"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "export":
		return runExport(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseID := fs.String("base-id", "", "OSProfiler base_id / trace id to export")
	configPath := fs.String("config", "", "Path to config YAML")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *baseID == "" {
		return fmt.Errorf("--base-id is required")
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}

	client := helper.NewClient(cfg.Helper.Command, cfg.OSProfiler.ConnectionString, cfg.Helper.RequestTimeout)
	if err := client.Start(); err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	reportJSON, err := client.GetReport(ctx, *baseID)
	if err != nil {
		return err
	}

	rep, err := report.Parse(reportJSON)
	if err != nil {
		return err
	}

	converted, err := otlp.Convert(rep, otlp.Options{
		BaseID:      *baseID,
		ServiceName: cfg.Bridge.ServiceName,
		Redaction: redaction.Options{
			RedactDBParams:      cfg.Bridge.RedactDBParams,
			RedactDBStatement:   cfg.Bridge.RedactDBStatement,
			RedactSensitiveKeys: cfg.Bridge.RedactSensitiveKeys,
		},
	})
	if err != nil {
		return err
	}

	exp := exporter.New(cfg.OTLP.Endpoint, cfg.OTLP.Timeout)
	if err := exp.Export(ctx, converted.Request); err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "exported base_id=%s spans=%d\n", *baseID, converted.SpanCount)
	return nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  osprofiler-tempo-bridge export --base-id <uuid> --config <path>

Commands:
  export    Export one OSProfiler trace by base_id`)
}
