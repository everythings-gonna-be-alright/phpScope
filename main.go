package main

import (
	"flag"
	"fmt"
	"os"
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
	// Command line flags configuration
	// Core settings
	pyroscopeURL := flag.String("pyroscopeUrl", "", "URL of the Pyroscope server")
	authToken := flag.String("auth", "", "Authentication token for Pyroscope")
	appName := flag.String("appName", "", "Application name for profiling data")
	excludeRegex := flag.String("exclude", "", "Regex pattern to exclude functions")
	var tags multiFlag
	flag.Var(&tags, "tags", "Tags in format key=value")

	// Profiling settings
	rateHz := flag.Int("rateHz", 400, "Sampling rate in Hz")
	interval := flag.Float64("interval", 0.1, "Time between data sends (seconds)")
	batchLimit := flag.Int("batch", 50000, "Maximum traces per batch")
	concurrentLimit := flag.Int("concurrent", 1, "Maximum concurrent requests")

	// PHP-specific settings
	phpspyBufferSize := flag.Int("phpspyBufferSize", 131072, "Size of phpspy's internal buffer")
	phpspyMaxDepth := flag.Int("phpspyMaxDepth", 50000, "Maximum stack trace depth")
	phpspyThreads := flag.Int("phpspyThreads", 64, "Number of phpspy worker threads")

	debug := flag.Bool("debug", false, "Enable debug logging")

	flag.Parse()

	if *pyroscopeURL == "" {
		fmt.Println("Error: pyroscope URL is required")
		os.Exit(1)
	}

	if *appName == "" {
		fmt.Println("Error: app name is required")
		os.Exit(1)
	}

	// Parse tags
	tagMap := make(map[string]string)
	for _, tag := range tags {
		key, value := parseTag(tag)
		tagMap[key] = value
	}

	// Print welcome banner with configuration
	printWelcomeBanner(*pyroscopeURL, *appName, *rateHz, *interval, *batchLimit, *concurrentLimit, tagMap, *excludeRegex, *debug)

	// Initialize sender with new configuration
	s := sender.New(sender.Config{
		PyroscopeURL: *pyroscopeURL,
		AuthToken:    *authToken,
		AppName:      *appName,
		RateHz:       *rateHz,
	})

	// Set global debug flag
	processor.Debug = *debug

	// Initialize processor without debug in config
	p := processor.New(processor.Config{
		Interval:         *interval,
		BatchLimit:       *batchLimit,
		RateHz:           *rateHz,
		ConcurrentLimit:  *concurrentLimit,
		Sender:           *s,
		ExcludePattern:   *excludeRegex,
		PhpspyBufferSize: *phpspyBufferSize,
		PhpspyMaxDepth:   *phpspyMaxDepth,
		PhpspyThreads:    *phpspyThreads,
	})

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
