package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"phpScope/config"
	"phpScope/processor"
	"phpScope/sender"
	"strings"
)

// authTokenEnv names the environment variable used when --auth is not set.
// Unlike command line arguments, it is not readable by other users in the
// shared process namespace of a pod.
const authTokenEnv = "PHPSCOPE_AUTH_TOKEN" //nolint:gosec // G101: name of the variable, not a credential

func printWelcomeBanner(pyroscopeURL, appName string, rateHz int, interval float64, batchLimit int, concurrentLimit int, tags map[string]string, sampleLabels []string, excludePattern string, pgrepPattern string, debug bool) {

	bannerLines := []string{
		"    ____  __  ______  _____                    ",
		"   / __ \\/ / / / __ \\/ ___/________  ____  ___ ",
		"  / /_/ / /_/ / /_/ /\\__ \\/ ___/ _ \\/ __ \\/ _ \\",
		" / ____/ __  / ____/___/ / /__/  __/ /_/ /  __/",
		"/_/   /_/ /_/_/    /____/\\___/\\___/ .___/\\___/ ",
		"                                 /_/            ",
	}

	// Print banner in orange color
	for _, line := range bannerLines {
		fmt.Println("\033[0;33m" + line + "\033[0m")
	}

	fmt.Print("https://github.com/everythings-gonna-be-alright\n\n")

	fmt.Println("🚀 Starting phpScope with configuration:")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📡 Pyroscope URL:      %s\n", pyroscopeURL)
	fmt.Printf("📝 Application Name:   %s\n", appName)
	fmt.Printf("⚡ Sample Rate:        %d Hz\n", rateHz)
	fmt.Printf("⏱️  Update Interval:    %.2f sec\n", interval)
	fmt.Printf("📦 Batch Limit:        %d\n", batchLimit)
	fmt.Printf("🔄 Concurrent Limit:   %d\n", concurrentLimit)
	fmt.Printf("🔍 Process Filter:     %s\n", pgrepPattern)
	if excludePattern != "" {
		fmt.Printf("🚫 Exclude Pattern:    %s\n", excludePattern)
	}
	if len(tags) > 0 {
		fmt.Printf("🏷️  Tags:\n")
		for k, v := range tags {
			fmt.Printf("   ├─ %s: %s\n", k, v)
		}
	}
	fmt.Printf("🔖 Sample Labels:      %s\n", strings.Join(sampleLabels, ","))
	fmt.Printf("🐛 Debug Mode:         %v\n", debug)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print welcome banner
	printWelcomeBanner(cfg.PyroscopeURL, cfg.AppName, cfg.RateHz, cfg.Interval, cfg.BatchLimit, cfg.ConcurrentLimit, cfg.Tags, cfg.SampleLabels, cfg.ExcludePattern, cfg.PhpspyPgrep, cfg.Debug)

	// Initialize components
	s := sender.New(cfg)
	p := processor.New(cfg, s)

	// Start processing
	if err := p.Process(); err != nil {
		fmt.Printf("Error processing: %v\n", err)
		os.Exit(1)
	}
}

// parseConfig builds and validates the configuration from command line arguments.
func parseConfig(args []string) (*config.Config, error) {
	cfg := config.NewDefault()
	fs := flag.NewFlagSet("phpscope", flag.ContinueOnError)

	// Command line flags configuration
	fs.StringVar(&cfg.PyroscopeURL, "pyroscopeUrl", "", "URL of the Pyroscope server")
	fs.StringVar(&cfg.AuthToken, "auth", "", "Authentication token for Pyroscope (visible in the process list; prefer the "+authTokenEnv+" environment variable)")
	fs.StringVar(&cfg.AppName, "appName", "", "Application name for profiling data")
	fs.StringVar(&cfg.ExcludePattern, "exclude", "", "Regex pattern to exclude functions")

	var tags multiFlag
	fs.Var(&tags, "tags", "Tags in format key=value")
	sampleLabels := fs.String("sampleLabels", strings.Join(cfg.SampleLabels, ","),
		"Comma-separated trace fields sent as per-sample labels (uri, path, qstring, cookie, ts, mem); every distinct value is a new Pyroscope series")

	// Profiling settings
	fs.IntVar(&cfg.RateHz, "rateHz", cfg.RateHz, "Sampling rate in Hz")
	fs.Float64Var(&cfg.Interval, "interval", cfg.Interval, "Time between data sends (seconds)")
	fs.IntVar(&cfg.BatchLimit, "batch", cfg.BatchLimit, "Maximum traces per batch")
	fs.IntVar(&cfg.ConcurrentLimit, "concurrent", cfg.ConcurrentLimit, "Maximum concurrent requests")

	// PHP-specific settings
	fs.IntVar(&cfg.PhpspyBufferSize, "phpspyBufferSize", cfg.PhpspyBufferSize, "Size of phpspy's internal buffer")
	fs.IntVar(&cfg.PhpspyMaxDepth, "phpspyMaxDepth", cfg.PhpspyMaxDepth, "Maximum stack trace depth")
	fs.IntVar(&cfg.PhpspyThreads, "phpspyThreads", cfg.PhpspyThreads, "Number of phpspy worker threads")
	fs.StringVar(&cfg.PhpspyRequestInfo, "phpspyRequestInfo", cfg.PhpspyRequestInfo, "Request info to include in traces")
	fs.StringVar(&cfg.PhpspyPgrep, "pgrep", cfg.PhpspyPgrep, "pgrep pattern for finding PHP processes")

	fs.BoolVar(&cfg.Debug, "debug", false, "Enable debug logging")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if cfg.AuthToken == "" {
		cfg.AuthToken = os.Getenv(authTokenEnv)
	}

	cfg.Tags = make(map[string]string, len(tags))
	for _, tag := range tags {
		key, value, err := parseTag(tag)
		if err != nil {
			return nil, err
		}
		cfg.Tags[key] = value
	}

	cfg.SampleLabels = nil
	for _, l := range strings.Split(*sampleLabels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			cfg.SampleLabels = append(cfg.SampleLabels, l)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// multiFlag implements flag.Value interface to support multiple flag values
// for the same flag (e.g., multiple -tags flags)
type multiFlag []string

func (f *multiFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *multiFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// parseTag splits a "key=value" string at the first "=" into key and value.
func parseTag(tag string) (string, string, error) {
	key, value, ok := strings.Cut(tag, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", fmt.Errorf("invalid tag %q: expected key=value", tag)
	}
	return key, strings.TrimSpace(value), nil
}
