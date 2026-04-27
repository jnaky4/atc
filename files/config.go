package files

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure for atc.
// Every field has a sensible default so the program works out of the box
// even if no config file exists.  The file is created automatically on
// first run so users always have a documented starting point.
type Config struct {
	Performance PerformanceConfig `yaml:"performance"`
	Display     DisplayConfig     `yaml:"display"`
	Logging     LoggingConfig     `yaml:"logging"`
}

// PerformanceConfig controls how aggressively atc uses system resources.
type PerformanceConfig struct {
	// RefreshIntervalMs is how often (in milliseconds) the display redraws
	// from background size updates.  Lower values feel more responsive;
	// higher values reduce CPU load during large tree scans.
	//
	// The scan worker count is NOT configurable here — it is always derived
	// from the machine at runtime (logical_cpus × 2).
	RefreshIntervalMs int `yaml:"refresh_interval_ms"`
}

// DisplayConfig controls what the user sees in the listing.
type DisplayConfig struct {
	// PageSize is the maximum number of directory entries shown at once
	// before the listing scrolls.
	PageSize int `yaml:"page_size"`

	// ShowHidden controls whether entries whose names begin with a dot
	// are included in the listing.
	ShowHidden bool `yaml:"show_hidden"`
}

// LoggingConfig controls diagnostic output.
type LoggingConfig struct {
	// Level is one of: error, warn, info, debug.
	// Only "warn" currently produces extra output (scan warnings); the
	// other levels are reserved for future structured logging.
	Level string `yaml:"level"`
}

// Cfg is the active configuration for the current process.
// It is populated by init() before any other package code runs.
var Cfg Config

// defaultConfig returns a Config populated entirely with safe defaults.
// It is used both as the seed when no file exists and as the fallback
// when the file cannot be parsed.
func defaultConfig() Config {
	return Config{
		Performance: PerformanceConfig{
			RefreshIntervalMs: 250,
		},
		Display: DisplayConfig{
			PageSize:   20,
			ShowHidden: true,
		},
		Logging: LoggingConfig{
			Level: "error",
		},
	}
}

// configPath returns the absolute path to the config file.
// It respects the XDG Base Directory specification: if $XDG_CONFIG_HOME is
// set that is used as the base; otherwise ~/.config is assumed.
func configPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Unusual but possible (e.g. no passwd entry).  Fall back to cwd.
			home = "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "atc", "config.yaml")
}

// configHeader is prepended to the YAML file so users understand the format
// without needing to read documentation.
const configHeader = `# atc — terminal file manager configuration
# Generated automatically on first run.  Edit as needed.
# Restart atc for changes to take effect.
#
# workers:             0        → auto (logical_cpus × 2); set >0 to override
# refresh_interval_ms: 250      → background size update rate in milliseconds
# page_size:           20       → items visible in the listing at once
# show_hidden:         true     → show dot-prefixed entries
# level:               error    → log verbosity: error | warn | info | debug

`

// loadOrCreateConfig loads configuration from disk and returns the result.
//
// Behaviour by case:
//   - File does not exist → write defaults to disk, return defaults.
//   - File exists, parses cleanly → return parsed config.
//   - File exists, cannot be read → log warning to stderr, return defaults.
//   - File exists, cannot be parsed → log warning to stderr, return defaults.
//
// In every case a usable Config is returned; the caller never needs to
// handle a nil or zero-value config.
func loadOrCreateConfig() Config {
	cfg := defaultConfig()
	path := configPath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		writeDefaultConfig(path, cfg)
		return cfg
	}
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"atc: cannot read config %s: %v — using defaults\n", path, err)
		return cfg
	}

	if parseErr := yaml.Unmarshal(data, &cfg); parseErr != nil {
		fmt.Fprintf(os.Stderr,
			"atc: cannot parse config %s: %v — using defaults\n", path, parseErr)
		return defaultConfig()
	}

	// Apply defaults for any field that was absent from the file
	// (yaml.Unmarshal leaves missing fields at their zero value).
	applyDefaults(&cfg)

	return cfg
}

// writeDefaultConfig serialises cfg to path, creating parent directories as
// needed.  Errors are logged to stderr but never returned — a missing config
// file is never fatal.
func writeDefaultConfig(path string, cfg Config) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr,
			"atc: cannot create config directory %s: %v\n", filepath.Dir(path), err)
		return
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		// yaml.Marshal only fails on un-marshallable types; our struct is safe.
		return
	}

	content := append([]byte(configHeader), out...)
	if err := os.WriteFile(path, content, 0644); err != nil {
		fmt.Fprintf(os.Stderr,
			"atc: cannot write default config to %s: %v\n", path, err)
	}
}

// applyDefaults fills in any zero-value fields that the YAML file omitted,
// ensuring we never operate with an invalid configuration even if the user
// partially edits the file.
func applyDefaults(cfg *Config) {
	d := defaultConfig()

	if cfg.Performance.RefreshIntervalMs <= 0 {
		cfg.Performance.RefreshIntervalMs = d.Performance.RefreshIntervalMs
	}
	if cfg.Display.PageSize <= 0 {
		cfg.Display.PageSize = d.Display.PageSize
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = d.Logging.Level
	}
}

// LoadConfig is a generic helper that unmarshals any YAML file into the
// provided structure pointer.  It is preserved for use by other parts of
// the codebase that need ad-hoc config loading.
func LoadConfig(filename string, structure interface{}) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, structure)
}
