package collector

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"phpScope/config"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxLineSize = 1024 * 1024

// Regular expressions for parsing different line formats
var (
	frameRegex = regexp.MustCompile(`^(\d+)\s+(.+)\s+([^:]+):(-?\d+)$`) // Matches stack frame lines
	tagRegex   = regexp.MustCompile(`^#\s*(\w+)\s*=\s*(.+)$`)           // Matches general tag lines
	memRegex   = regexp.MustCompile(`^#\s*mem\s+(\d+)\s+(\d+)$`)        // Matches memory usage lines
)

// New creates a new PhpSpy instance with the provided configuration
func New(cfg *config.Config) *PhpSpy {
	ps := &PhpSpy{
		config:       cfg,
		sampleLabels: make(map[string]bool, len(cfg.SampleLabels)),
		traces:       make(chan *Trace, cfg.BatchLimit*2),
		stopped:      make(chan struct{}),
	}
	for _, l := range cfg.SampleLabels {
		ps.sampleLabels[l] = true
	}

	if cfg.ExcludePattern != "" {
		re, err := regexp.Compile(cfg.ExcludePattern)
		if err != nil {
			log.Printf("Warning: invalid exclude pattern: %v\n", err)
		} else {
			ps.excludeRegex = re
		}
	}

	return ps
}

// phpspyArgs builds the phpspy argument list. Arguments are passed directly to
// exec, never through a shell, so config values cannot inject commands.
func (ps *PhpSpy) phpspyArgs() []string {
	return []string{
		fmt.Sprintf("--rate-hz=%d", ps.config.RateHz),
		"--pgrep=" + ps.config.PhpspyPgrep,
		fmt.Sprintf("--buffer-size=%d", ps.config.PhpspyBufferSize),
		fmt.Sprintf("--max-depth=%d", ps.config.PhpspyMaxDepth),
		fmt.Sprintf("--threads=%d", ps.config.PhpspyThreads),
		"--request-info=" + ps.config.PhpspyRequestInfo,
	}
}

// Start begins collecting PHP stack traces using phpspy
// Returns a channel that will receive parsed traces and any error that occurred
func (ps *PhpSpy) Start() (<-chan *Trace, error) {
	cmd := exec.Command("phpspy", ps.phpspyArgs()...) //nolint:gosec // G204: fixed binary, args passed without a shell

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("error creating stdout pipe: %v", err)
	}

	log.Printf("Starting process: %v\n", cmd.String())
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error starting phpspy: %v", err)
	}

	go func() {
		defer close(ps.traces)
		ps.readTraces(stdout)
		// Wait closes the pipe, so it must only run after all output is read.
		if err := cmd.Wait(); err != nil {
			log.Fatalf("phpspy process exited with error: %v", err)
		}
	}()

	return ps.traces, nil
}

// Read parses phpspy output from r in the background and returns the channel
// of parsed traces. The channel is closed when r is exhausted.
func (ps *PhpSpy) Read(r io.Reader) <-chan *Trace {
	go func() {
		defer close(ps.traces)
		ps.readTraces(r)
	}()
	return ps.traces
}

// readTraces parses phpspy output line by line until r is exhausted,
// sending each completed trace to ps.traces.
func (ps *PhpSpy) readTraces(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	var currentTrace []string
	var lastTraceTime time.Time
	firstTrace := true
	period := ps.samplePeriod()

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
					// After an idle gap the previous trace is stale; a single sample
					// never covers more than one sampling period.
					if earliest := trace.TsEndProcessing.Add(-period); period > 0 && trace.TsStartProcessing.Before(earliest) {
						trace.TsStartProcessing = earliest
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
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading phpspy output: %v\n", err)
	}
}

// samplePeriod is the interval between phpspy samples, or 0 if no rate is set.
func (ps *PhpSpy) samplePeriod() time.Duration {
	if ps.config.RateHz <= 0 {
		return 0
	}
	return time.Second / time.Duration(ps.config.RateHz)
}

// parseTrace processes a slice of strings representing a single stack trace
// and converts it into a structured Trace object
func (ps *PhpSpy) parseTrace(traces []string) (*Trace, error) {
	trace := Trace{
		Frames: []StackFrame{},
		Tags:   make(map[string][]string),
	}

	// Process each line of the trace
	for _, line := range traces {
		line = strings.ToValidUTF8(line, "\uFFFD")
		if matches := frameRegex.FindStringSubmatch(line); matches != nil {
			// Skip frames matching exclude pattern
			if ps.excludeRegex != nil && ps.excludeRegex.MatchString(line) {
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
			if ps.sampleLabels["mem"] {
				trace.Tags["mem"] = []string{matches[1] + " " + matches[2]}
			}
		} else if matches := tagRegex.FindStringSubmatch(line); matches != nil {
			// Parse general tag information
			if ps.sampleLabels[matches[1]] {
				trace.Tags[matches[1]] = []string{matches[2]}
			}
		}
	}

	return &trace, nil
}
