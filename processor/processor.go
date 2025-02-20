package processor

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"phpScope/sender"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/pprof/profile"
)

// Debug controls global debug logging
var Debug bool

// Config holds the configuration for the profile processor
type Config struct {
	Interval         float64       // Time between batch sends in seconds
	RateHz           int           // Sampling rate in Hz
	BatchLimit       int           // Maximum number of traces per batch
	ConcurrentLimit  int           // Maximum number of concurrent requests
	Sender           sender.Sender // Sender instance for sending profiles
	PluginPaths      []string      // Paths to plugins (currently unused)
	ExcludePattern   string        // Regex pattern for functions to exclude
	PhpspyBufferSize int           // Size of phpspy's internal buffer
	PhpspyMaxDepth   int           // Maximum stack trace depth
	PhpspyThreads    int           // Number of phpspy worker threads
}

// Processor handles the collection and processing of PHP profiling data
type Processor struct {
	config      Config
	bulkAmmount int        // Current number of traces in the batch
	mu          sync.Mutex // Mutex for thread-safe counter updates
}

// StackFrame represents a single frame in a PHP stack trace
type StackFrame struct {
	Index     int    // Frame position in the stack (0 is most recent)
	Method    string // Name of the PHP function/method
	File      string // Source file path
	StartLine int    // Line number in the source file
}

// Trace represents a complete PHP stack trace with metadata
type Trace struct {
	Frames            []StackFrame        // Stack frames in the trace
	Tags              map[string][]string // Metadata tags for the trace
	tsStartProcessing time.Time           // Start time of trace processing
	tsEndProcessing   time.Time           // End time of trace processing
}

// Sample type configuration for the profiler
var sampleTypeConfig = map[string]map[string]interface{}{
	"cpu": {
		"units":        "nanoseconds",
		"display-name": "cpu-time",
		"aggregation":  "sum",
		"cumulative":   false,
		"sampled":      true,
	},
	"samples": {
		"units":        "count",
		"display-name": "sample-count",
		"aggregation":  "sum",
		"cumulative":   false,
		"sampled":      true,
	},
	// "memory": {
	// 	"units":       "bytes",
	// 	"aggregation": "avg",
	// 	"sampled":     false,
	// },
}

// New creates a new Processor instance with the given configuration
func New(config Config) *Processor {
	return &Processor{
		config:      config,
		bulkAmmount: 0,
	}
}

// Process starts the main processing pipeline:
// 1. Collects traces from phpspy
// 2. Processes traces in batches
// 3. Converts traces to pprof format
// 4. Sends profiles to Pyroscope
func (p *Processor) Process() error {
	// Create buffered channels for processing pipeline
	traces := make(chan *Trace, p.config.BatchLimit*2)
	pprofProfiles := make(chan *profile.Profile, p.config.ConcurrentLimit)

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(traces) // Close channel after producer completion
		p.producer(traces)
	}()

	// Start processors
	for i := 0; i < p.config.ConcurrentLimit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.processTraces(traces, pprofProfiles)
		}()
	}

	// Start consumer
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(pprofProfiles) // Close channel after all processors complete
		p.consumer(pprofProfiles)
	}()

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	}
}

// Producer runs phpspy and collects stack traces
func (p *Processor) producer(traces chan<- *Trace) {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("phpspy --rate-hz=%d --pgrep='-x \"(php-fpm.*|^php$)\"' --buffer-size=%d --max-depth=%d --threads=%d --request-info=qcup",
		p.config.RateHz,
		p.config.PhpspyBufferSize,
		p.config.PhpspyMaxDepth,
		p.config.PhpspyThreads))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating stdout pipe: %v\n", err)
	}

	log.Printf("Starting process: %v\n", cmd.String())
	if err := cmd.Start(); err != nil {
		log.Fatalf("Error starting sh: %v\n", err)
	}
	// Check if the command is running
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Fatalf("phpspy process exited with error: %v", err)
		}
	}()

	// Create scanner to read output
	scanner := bufio.NewScanner(stdout)

	var currentTrace []string

	// End time of last trace
	var lastTraceTime time.Time
	firstTrace := true

	for scanner.Scan() {
		if firstTrace {
			lastTraceTime = time.Now()
		}

		line := strings.TrimSpace(scanner.Text())

		// Empty line marks end of trace
		if line == "" {
			if len(currentTrace) > 0 {
				trace, err := parseTrace(currentTrace, p.config.ExcludePattern)
				if err == nil {
					trace.tsEndProcessing = time.Now()
					if !lastTraceTime.IsZero() {
						trace.tsStartProcessing = lastTraceTime
					}
					lastTraceTime = trace.tsEndProcessing
					traces <- trace
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
}

// processTraces collects traces into batches and triggers processing
func (p *Processor) processTraces(traces <-chan *Trace, pprofProfiles chan<- *profile.Profile) {
	var tracesForProcessing []Trace

	timer := time.NewTimer(time.Duration(p.config.Interval) * time.Second)
	defer timer.Stop()

	for {
		select {
		case trace, ok := <-traces:
			if !ok {
				if len(tracesForProcessing) > 0 {
					p.processBatch(tracesForProcessing, pprofProfiles)
				}
				return
			}

			tracesForProcessing = append(tracesForProcessing, *trace)

			p.mu.Lock()
			p.bulkAmmount++
			currentBulkAmount := p.bulkAmmount
			p.mu.Unlock()

			if currentBulkAmount >= p.config.BatchLimit {
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
				p.refreshCounters()
				timer.Reset(time.Duration(p.config.Interval) * time.Second)
			}

		case <-timer.C:
			if len(tracesForProcessing) > 0 {
				// Process current batch
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
				p.refreshCounters()
			}
			nextInterval := time.Duration(p.config.Interval)*time.Second + 100*time.Millisecond
			timer.Reset(nextInterval)
		}
	}
}

// parseTrace converts phpspy output into a structured Trace
func parseTrace(traces []string, excludePattern string) (*Trace, error) {
	trace := Trace{
		Frames: []StackFrame{},
		Tags:   make(map[string][]string),
	}

	frameRegex := regexp.MustCompile(`^(\d+)\s+(.+)\s+([^:]+):(-?\d+)$`)
	tagRegex := regexp.MustCompile(`^#\s*(\w+)\s*=\s*(.+)$`)
	memRegex := regexp.MustCompile(`^#\s*mem\s+(\d+)\s+(\d+)$`)

	var excludeRegex *regexp.Regexp
	if excludePattern != "" {
		var err error
		excludeRegex, err = regexp.Compile(excludePattern)
		if err != nil {
			log.Printf("Warning: invalid exclude pattern: %v\n", err)
		}
	}

	for _, line := range traces {
		if matches := frameRegex.FindStringSubmatch(line); matches != nil {
			// Skip if the line matches exclude pattern
			if excludeRegex != nil && excludeRegex.MatchString(line) {
				continue
			}

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
			trace.Tags["mem"] = []string{matches[1] + " " + matches[2]}
		} else if matches := tagRegex.FindStringSubmatch(line); matches != nil {
			trace.Tags[matches[1]] = []string{matches[2]}
		}
	}

	return &trace, nil
}

// processBatch converts a batch of traces into a pprof profile
func (p *Processor) processBatch(traces []Trace, pprofProfiles chan<- *profile.Profile) {
	convertTraceToPprof(traces, pprofProfiles, p.config.RateHz)
}

// convertTraceToPprof converts PHP stack traces to pprof format
// It calculates timing information and creates a profile.Profile
func convertTraceToPprof(traces []Trace, pprofTraces chan<- *profile.Profile, sampleRate int) {
	samplesCount := int64(len(traces))

	if samplesCount == 0 {
		return
	}

	firstSampleTime := traces[0].tsStartProcessing
	lastSampleTime := traces[len(traces)-1].tsEndProcessing
	actualDuration := lastSampleTime.Sub(firstSampleTime)

	if Debug {
		log.Printf("Batch stats: start=%v end=%v duration=%v samples=%d rate=%d/s",
			firstSampleTime.Format(time.RFC3339Nano),
			lastSampleTime.Format(time.RFC3339Nano),
			actualDuration,
			samplesCount,
			sampleRate)
	}

	totalCPUTime := actualDuration.Nanoseconds()
	timePerSample := totalCPUTime / samplesCount

	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "cpu", Unit: "nanoseconds"},
			{Type: "samples", Unit: "count"},
			// {Type: "memory_size", Unit: "bytes"},
		},
		TimeNanos:     firstSampleTime.UnixNano(),
		DurationNanos: actualDuration.Nanoseconds(),
		PeriodType: &profile.ValueType{
			Type: "cpu",
			Unit: "nanoseconds",
		},
		Period: timePerSample,
	}

	functions := make(map[string]*profile.Function)
	locations := make(map[string]*profile.Location)
	nextFuncID := uint64(1)
	nextLocID := uint64(1)

	for _, trace := range traces {
		var sampleLocations []*profile.Location
		// memorySize := int64(0)
		// if memValues, ok := trace.Tags["mem"]; ok && len(memValues) > 0 {
		// 	memParts := strings.Fields(memValues[0])
		// 	if len(memParts) >= 2 {
		// 		if size, err := strconv.ParseInt(memParts[0], 10, 64); err == nil {
		// 			memorySize = size
		// 		}
		// 	}
		// }

		for _, frame := range trace.Frames {
			functionKey := frame.Method + frame.File

			if _, exists := functions[functionKey]; !exists {
				functions[functionKey] = &profile.Function{
					ID:         nextFuncID,
					Name:       frame.Method,
					SystemName: frame.Method,
					Filename:   frame.File,
					StartLine:  int64(frame.StartLine),
				}
				prof.Function = append(prof.Function, functions[functionKey])
				nextFuncID++
			}

			locationKey := fmt.Sprintf("%s:%d", functionKey, frame.StartLine)
			if _, exists := locations[locationKey]; !exists {
				locations[locationKey] = &profile.Location{
					ID: nextLocID,
					Line: []profile.Line{
						{
							Function: functions[functionKey],
							Line:     int64(frame.StartLine),
						},
					},
				}
				prof.Location = append(prof.Location, locations[locationKey])
				nextLocID++
			}

			sampleLocations = append(sampleLocations, locations[locationKey])
		}

		// Calculate real time for specific sample
		sampleDuration := trace.tsEndProcessing.Sub(trace.tsStartProcessing)
		sampleTime := sampleDuration.Nanoseconds()

		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: sampleLocations,
			Value:    []int64{sampleTime, 1},
			Label:    trace.Tags,
		})
	}

	pprofTraces <- prof
}

// consumer receives pprof profiles and sends them to Pyroscope
func (p *Processor) consumer(pprofProfiles <-chan *profile.Profile) {
	for profile := range pprofProfiles {
		if err := p.config.Sender.SendSample(profile, sampleTypeConfig); err != nil {
			fmt.Printf("Error sending sample: %v\n", err)
		}
	}
	log.Println("Sendet")
}

// refreshCounters resets the batch counters
func (p *Processor) refreshCounters() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bulkAmmount = 0
}
