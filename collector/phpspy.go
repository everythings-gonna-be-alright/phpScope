package collector

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"phpScope/config"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// New creates a new PhpSpy instance with the provided configuration
func New(cfg *config.Config) *PhpSpy {
	return &PhpSpy{
		config:  cfg,
		traces:  make(chan *Trace, cfg.BatchLimit*2),
		stopped: make(chan struct{}),
	}
}

// Start begins collecting PHP stack traces using phpspy
// Returns a channel that will receive parsed traces and any error that occurred
func (ps *PhpSpy) Start() (<-chan *Trace, error) {
	// Construct phpspy command with configuration parameters
	cmd := exec.Command("sh", "-c", fmt.Sprintf("phpspy --rate-hz=%d --pgrep='-x \"(php-fpm.*|^php$)\"' --buffer-size=%d --max-depth=%d --threads=%d --request-info=%d",
		ps.config.RateHz,
		ps.config.PhpspyBufferSize,
		ps.config.PhpspyMaxDepth,
		ps.config.PhpspyThreads,
		ps.config.PhpspyRequestInfo))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("error creating stdout pipe: %v", err)
	}

	log.Printf("Starting process: %v\n", cmd.String())
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error starting sh: %v", err)
	}

	go func() {
		// Monitor phpspy process for errors
		err := cmd.Wait()
		if err != nil {
			log.Fatalf("phpspy process exited with error: %v", err)
		}
	}()

	go func() {
		defer close(ps.traces)
		scanner := bufio.NewScanner(stdout)
		var currentTrace []string
		var lastTraceTime time.Time
		firstTrace := true

		// Process phpspy output line by line
		for scanner.Scan() {
			if firstTrace {
				lastTraceTime = time.Now()
			}

			line := strings.TrimSpace(scanner.Text())

			// Empty line indicates end of current trace
			if line == "" {
				if len(currentTrace) > 0 {
					trace, err := ps.parseTrace(currentTrace)
					if err == nil {
						trace.TsEndProcessing = time.Now()
						if !lastTraceTime.IsZero() {
							trace.TsStartProcessing = lastTraceTime
						}
						lastTraceTime = trace.TsEndProcessing
						ps.traces <- trace
					} else {
						log.Printf("Error parsing trace: %v\n", err)
					}
					currentTrace = nil
				} else {
					firstTrace = true
				}
				continue
			}

			currentTrace = append(currentTrace, line)
			firstTrace = false
		}
	}()

	return ps.traces, nil
}

// parseTrace processes a slice of strings representing a single stack trace
// and converts it into a structured Trace object
func (ps *PhpSpy) parseTrace(traces []string) (*Trace, error) {
	trace := Trace{
		Frames: []StackFrame{},
		Tags:   make(map[string][]string),
	}

	// Regular expressions for parsing different line formats
	frameRegex := regexp.MustCompile(`^(\d+)\s+(.+)\s+([^:]+):(-?\d+)$`) // Matches stack frame lines
	tagRegex := regexp.MustCompile(`^#\s*(\w+)\s*=\s*(.+)$`)             // Matches general tag lines
	memRegex := regexp.MustCompile(`^#\s*mem\s+(\d+)\s+(\d+)$`)          // Matches memory usage lines

	// Compile exclude pattern if configured
	var excludeRegex *regexp.Regexp
	if ps.config.ExcludePattern != "" {
		var err error
		excludeRegex, err = regexp.Compile(ps.config.ExcludePattern)
		if err != nil {
			log.Printf("Warning: invalid exclude pattern: %v\n", err)
		}
	}

	// Process each line of the trace
	for _, line := range traces {
		if matches := frameRegex.FindStringSubmatch(line); matches != nil {
			// Skip frames matching exclude pattern
			if excludeRegex != nil && excludeRegex.MatchString(line) {
				continue
			}

			// Parse stack frame information
			index, _ := strconv.Atoi(matches[1])
			startLine, _ := strconv.Atoi(matches[4])
			frame := StackFrame{
				Index:     index,
				Method:    matches[2],
				File:      matches[3],
				StartLine: startLine,
			}
			trace.Frames = append(trace.Frames, frame)
		} else if matches := memRegex.FindStringSubmatch(line); matches != nil {
			// Parse memory usage information
			trace.Tags["mem"] = []string{matches[1] + " " + matches[2]}
		} else if matches := tagRegex.FindStringSubmatch(line); matches != nil {
			// Parse general tag information
			trace.Tags[matches[1]] = []string{matches[2]}
		}
	}

	return &trace, nil
}
