package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/aolda/aolda-trace-bridge/internal/config"
	"github.com/aolda/aolda-trace-bridge/internal/exporter"
	"github.com/aolda/aolda-trace-bridge/internal/helper"
	"github.com/aolda/aolda-trace-bridge/internal/otlp"
	"github.com/aolda/aolda-trace-bridge/internal/redaction"
	"github.com/aolda/aolda-trace-bridge/internal/report"
	"github.com/aolda/aolda-trace-bridge/internal/state"
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
	case "watch":
		return runWatch(args[1:])
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
	exp := exporter.New(cfg.OTLP.Endpoint, cfg.OTLP.Timeout)
	spanCount, err := exportTrace(ctx, client, exp, *baseID, cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "exported base_id=%s spans=%d\n", *baseID, spanCount)
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "Path to config YAML")
	once := fs.Bool("once", false, "Run one poll cycle and exit")
	if err := fs.Parse(args); err != nil {
		return err
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

	exp := exporter.New(cfg.OTLP.Endpoint, cfg.OTLP.Timeout)
	store, err := state.Load(cfg.Watch.StateFile)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		if err := pollAndExport(ctx, client, exp, store, cfg); err != nil {
			return err
		}
		if *once {
			return nil
		}

		timer := time.NewTimer(cfg.Watch.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func exportTrace(ctx context.Context, client *helper.Client, exp exporter.Exporter, baseID string, cfg config.Config) (int, error) {
	reportJSON, err := client.GetReport(ctx, baseID)
	if err != nil {
		return 0, err
	}

	rep, err := report.Parse(reportJSON)
	if err != nil {
		return 0, err
	}

	converted, err := otlp.Convert(rep, otlp.Options{
		BaseID:      baseID,
		ServiceName: cfg.Bridge.ServiceName,
		Redaction: redaction.Options{
			RedactDBParams:      cfg.Bridge.RedactDBParams,
			RedactDBStatement:   cfg.Bridge.RedactDBStatement,
			RedactSensitiveKeys: cfg.Bridge.RedactSensitiveKeys,
		},
	})
	if err != nil {
		return 0, err
	}

	if err := exp.Export(ctx, converted.Request); err != nil {
		return 0, err
	}

	return converted.SpanCount, nil
}

func pollAndExport(ctx context.Context, client *helper.Client, exp exporter.Exporter, store *state.Store, cfg config.Config) error {
	traces, err := client.ListTraces(ctx)
	if err != nil {
		return err
	}

	remaining := cfg.Watch.MaxTracesPerPoll
	if cfg.Watch.DeleteAfterExport {
		remaining = retryPendingDeletes(ctx, client, store, traces, remaining)
		if remaining <= 0 {
			return nil
		}
	}

	traces = eligibleTraces(traces, store, cfg.Watch.ExportDelay, time.Now().UTC())
	if len(traces) > remaining {
		traces = traces[:remaining]
	}

	for _, trace := range traces {
		if ctx.Err() != nil {
			return nil
		}

		spanCount, err := exportTrace(ctx, client, exp, trace.BaseID, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "watch export_failed base_id=%s error=%v\n", trace.BaseID, err)
			continue
		}

		if err := store.MarkExported(trace.BaseID, spanCount); err != nil {
			return err
		}

		if cfg.Watch.DeleteAfterExport {
			deleted, err := client.DeleteTrace(ctx, trace.BaseID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "watch delete_failed base_id=%s error=%v\n", trace.BaseID, err)
				continue
			}
			if err := store.MarkDeleted(trace.BaseID, deleted); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "watch exported base_id=%s spans=%d deleted=%d\n", trace.BaseID, spanCount, deleted)
			continue
		}

		fmt.Fprintf(os.Stdout, "watch exported base_id=%s spans=%d\n", trace.BaseID, spanCount)
	}
	return nil
}

func retryPendingDeletes(ctx context.Context, client *helper.Client, store *state.Store, traces []helper.TraceSummary, remaining int) int {
	seen := map[string]bool{}
	for _, trace := range traces {
		if remaining <= 0 || ctx.Err() != nil {
			return remaining
		}
		if trace.BaseID == "" || seen[trace.BaseID] {
			continue
		}
		seen[trace.BaseID] = true

		if !store.IsExported(trace.BaseID) || store.IsDeleted(trace.BaseID) {
			continue
		}

		deleted, err := client.DeleteTrace(ctx, trace.BaseID)
		remaining--
		if err != nil {
			fmt.Fprintf(os.Stderr, "watch delete_failed base_id=%s error=%v\n", trace.BaseID, err)
			continue
		}
		if err := store.MarkDeleted(trace.BaseID, deleted); err != nil {
			fmt.Fprintf(os.Stderr, "watch state_failed base_id=%s error=%v\n", trace.BaseID, err)
			continue
		}
		fmt.Fprintf(os.Stdout, "watch deleted_exported base_id=%s deleted=%d\n", trace.BaseID, deleted)
	}
	return remaining
}

func eligibleTraces(traces []helper.TraceSummary, store *state.Store, exportDelay time.Duration, now time.Time) []helper.TraceSummary {
	seen := map[string]bool{}
	var out []helper.TraceSummary
	for _, trace := range traces {
		if trace.BaseID == "" || seen[trace.BaseID] || store.IsExported(trace.BaseID) {
			continue
		}
		seen[trace.BaseID] = true

		if trace.Timestamp != "" && exportDelay > 0 {
			timestamp, err := otlp.ParseTimestamp(trace.Timestamp)
			if err == nil && timestamp.After(now.Add(-exportDelay)) {
				continue
			}
		}
		out = append(out, trace)
	}

	sort.SliceStable(out, func(i, j int) bool {
		left, leftErr := otlp.ParseTimestamp(out[i].Timestamp)
		right, rightErr := otlp.ParseTimestamp(out[j].Timestamp)
		switch {
		case leftErr == nil && rightErr == nil:
			return left.Before(right)
		case leftErr == nil:
			return true
		case rightErr == nil:
			return false
		default:
			return out[i].BaseID < out[j].BaseID
		}
	})

	return out
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage:
  osprofiler-tempo-bridge export --base-id <uuid> --config <path>
  osprofiler-tempo-bridge watch --config <path> [--once]

Commands:
  export    Export one OSProfiler trace by base_id
  watch     Poll OSProfiler traces and export them in bounded batches`)
}
