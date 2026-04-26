package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	OSProfiler OSProfilerConfig
	Helper     HelperConfig
	OTLP       OTLPConfig
	Bridge     BridgeConfig
	Watch      WatchConfig
	Metrics    MetricsConfig
}

type OSProfilerConfig struct {
	ConnectionString string
}

type HelperConfig struct {
	Command        []string
	RequestTimeout time.Duration
	StartupTimeout time.Duration
}

type OTLPConfig struct {
	Endpoint string
	Timeout  time.Duration
}

type BridgeConfig struct {
	ServiceName         string
	RedactDBParams      bool
	RedactDBStatement   bool
	RedactSensitiveKeys bool
}

type WatchConfig struct {
	PollInterval      time.Duration
	ExportDelay       time.Duration
	StateFile         string
	MaxTracesPerPoll  int
	DeleteAfterExport bool
}

type MetricsConfig struct {
	ListenAddr string
	Path       string
}

type rawConfig struct {
	OSProfiler struct {
		ConnectionString string `yaml:"connection_string"`
	} `yaml:"osprofiler"`
	Helper struct {
		Command        []string `yaml:"command"`
		RequestTimeout string   `yaml:"request_timeout"`
		StartupTimeout string   `yaml:"startup_timeout"`
	} `yaml:"helper"`
	OTLP struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	} `yaml:"otlp"`
	Bridge struct {
		ServiceName         string `yaml:"service_name"`
		RedactDBParams      *bool  `yaml:"redact_db_params"`
		RedactDBStatement   *bool  `yaml:"redact_db_statement"`
		RedactSensitiveKeys *bool  `yaml:"redact_sensitive_keys"`
	} `yaml:"bridge"`
	Watch struct {
		PollInterval      string `yaml:"poll_interval"`
		ExportDelay       string `yaml:"export_delay"`
		StateFile         string `yaml:"state_file"`
		MaxTracesPerPoll  int    `yaml:"max_traces_per_poll"`
		DeleteAfterExport *bool  `yaml:"delete_after_export"`
	} `yaml:"watch"`
	Metrics struct {
		ListenAddr string `yaml:"listen_addr"`
		Path       string `yaml:"path"`
	} `yaml:"metrics"`
}

func LoadFile(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var raw rawConfig
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return Config{}, fmt.Errorf("parse config yaml: %w", err)
	}

	cfg := Config{
		OSProfiler: OSProfilerConfig{
			ConnectionString: raw.OSProfiler.ConnectionString,
		},
		Helper: HelperConfig{
			Command: raw.Helper.Command,
		},
		OTLP: OTLPConfig{
			Endpoint: raw.OTLP.Endpoint,
		},
		Bridge: BridgeConfig{
			ServiceName:         valueOr(raw.Bridge.ServiceName, "osprofiler-bridge"),
			RedactDBParams:      boolOr(raw.Bridge.RedactDBParams, true),
			RedactDBStatement:   boolOr(raw.Bridge.RedactDBStatement, false),
			RedactSensitiveKeys: boolOr(raw.Bridge.RedactSensitiveKeys, true),
		},
		Watch: WatchConfig{
			StateFile:         valueOr(raw.Watch.StateFile, "/var/lib/osprofiler-tempo-bridge/state.json"),
			MaxTracesPerPoll:  intOr(raw.Watch.MaxTracesPerPoll, 100),
			DeleteAfterExport: boolOr(raw.Watch.DeleteAfterExport, true),
		},
		Metrics: MetricsConfig{
			ListenAddr: valueOr(raw.Metrics.ListenAddr, ":9090"),
			Path:       valueOr(raw.Metrics.Path, "/metrics"),
		},
	}

	cfg.Helper.RequestTimeout, err = parseDurationOr(raw.Helper.RequestTimeout, 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("helper.request_timeout: %w", err)
	}
	cfg.Helper.StartupTimeout, err = parseDurationOr(raw.Helper.StartupTimeout, 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("helper.startup_timeout: %w", err)
	}
	cfg.OTLP.Timeout, err = parseDurationOr(raw.OTLP.Timeout, 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("otlp.timeout: %w", err)
	}
	cfg.Watch.PollInterval, err = parseDurationOr(raw.Watch.PollInterval, 30*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("watch.poll_interval: %w", err)
	}
	cfg.Watch.ExportDelay, err = parseDurationOr(raw.Watch.ExportDelay, 2*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("watch.export_delay: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.OSProfiler.ConnectionString == "" {
		return errors.New("osprofiler.connection_string is required")
	}
	if len(c.Helper.Command) == 0 {
		return errors.New("helper.command is required")
	}
	if c.Helper.RequestTimeout <= 0 {
		return errors.New("helper.request_timeout must be positive")
	}
	if c.Helper.StartupTimeout <= 0 {
		return errors.New("helper.startup_timeout must be positive")
	}
	if c.OTLP.Endpoint == "" {
		return errors.New("otlp.endpoint is required")
	}
	if c.OTLP.Timeout <= 0 {
		return errors.New("otlp.timeout must be positive")
	}
	if c.Bridge.ServiceName == "" {
		return errors.New("bridge.service_name is required")
	}
	if c.Watch.PollInterval <= 0 {
		return errors.New("watch.poll_interval must be positive")
	}
	if c.Watch.ExportDelay < 0 {
		return errors.New("watch.export_delay must not be negative")
	}
	if c.Watch.StateFile == "" {
		return errors.New("watch.state_file is required")
	}
	if c.Watch.MaxTracesPerPoll <= 0 {
		return errors.New("watch.max_traces_per_poll must be positive")
	}
	if c.Metrics.ListenAddr == "" {
		return errors.New("metrics.listen_addr is required")
	}
	if c.Metrics.Path == "" {
		return errors.New("metrics.path is required")
	}
	return nil
}

func parseDurationOr(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	return time.ParseDuration(value)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolOr(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func intOr(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
