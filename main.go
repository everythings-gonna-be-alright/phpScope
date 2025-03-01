package main

import (
	"flag"
	"fmt"
	"os"
	"phpScope/config"
	"phpScope/processor"
	"phpScope/sender"
	"strings"
)

func printWelcomeBanner(pyroscopeURL, appName string, rateHz int, interval float64, batchLimit int, concurrentLimit int, tags map[string]string, excludePattern string, debug bool) {

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
	if excludePattern != "" {
		fmt.Printf("🚫 Exclude Pattern:    %s\n", excludePattern)
	}
	if len(tags) > 0 {
		fmt.Printf("🏷️  Tags:\n")
		for k, v := range tags {
			fmt.Printf("   ├─ %s: %s\n", k, v)
		}
	}
	fmt.Printf("🐛 Debug Mode:         %v\n", debug)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

func main() {
	cfg := config.NewDefault()

	// Command line flags configuration
	flag.StringVar(&cfg.PyroscopeURL, "pyroscopeUrl", "", "URL of the Pyroscope server")
	flag.StringVar(&cfg.AuthToken, "auth", "", "Authentication token for Pyroscope")
	flag.StringVar(&cfg.AppName, "appName", "", "Application name for profiling data")
	flag.StringVar(&cfg.ExcludePattern, "exclude", "", "Regex pattern to exclude functions")

	var tags multiFlag
	flag.Var(&tags, "tags", "Tags in format key=value")

	// Profiling settings
	flag.IntVar(&cfg.RateHz, "rateHz", cfg.RateHz, "Sampling rate in Hz")
	flag.Float64Var(&cfg.Interval, "interval", cfg.Interval, "Time between data sends (seconds)")
	flag.IntVar(&cfg.BatchLimit, "batch", cfg.BatchLimit, "Maximum traces per batch")
	flag.IntVar(&cfg.ConcurrentLimit, "concurrent", cfg.ConcurrentLimit, "Maximum concurrent requests")

	// PHP-specific settings
	flag.IntVar(&cfg.PhpspyBufferSize, "phpspyBufferSize", cfg.PhpspyBufferSize, "Size of phpspy's internal buffer")
	flag.IntVar(&cfg.PhpspyMaxDepth, "phpspyMaxDepth", cfg.PhpspyMaxDepth, "Maximum stack trace depth")
	flag.IntVar(&cfg.PhpspyThreads, "phpspyThreads", cfg.PhpspyThreads, "Number of phpspy worker threads")
	flag.StringVar(&cfg.PhpspyRequestInfo, "phpspyRequestInfo", cfg.PhpspyRequestInfo, "Request info to include in traces")

	flag.BoolVar(&cfg.Debug, "debug", false, "Enable debug logging")

	flag.Parse()

	// Validate required fields
	if cfg.PyroscopeURL == "" {
		fmt.Println("Error: pyroscope URL is required")
		os.Exit(1)
	}

	if cfg.AppName == "" {
		fmt.Println("Error: app name is required")
		os.Exit(1)
	}

	// Parse tags
	cfg.Tags = make(map[string]string)
	for _, tag := range tags {
		key, value := parseTag(tag)
		cfg.Tags[key] = value
	}

	// Print welcome banner
	printWelcomeBanner(cfg.PyroscopeURL, cfg.AppName, cfg.RateHz, cfg.Interval, cfg.BatchLimit, cfg.ConcurrentLimit, cfg.Tags, cfg.ExcludePattern, cfg.Debug)

	// Initialize components
	s := sender.New(cfg)
	p := processor.New(cfg, s)

	// Start processing
	if err := p.Process(); err != nil {
		fmt.Printf("Error processing: %v\n", err)
		os.Exit(1)
	}
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

// parseTag splits a "key=value" string into separate key and value.
// Returns empty strings if the format is invalid.
func parseTag(tag string) (string, string) {
	parts := strings.Split(tag, "=")
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}
