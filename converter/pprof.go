package converter

import (
	"fmt"
	"log"
	"phpScope/collector"
	"time"

	"github.com/google/pprof/profile"
)

// ConvertTracesToPprof converts PHP stack traces to pprof format.
// Parameters:
//   - traces: Slice of PHP stack traces to convert
//   - sampleRate: Number of samples collected per second
//   - debug: Enable detailed logging of conversion process
//
// Returns a profile.Profile or nil if no traces provided
func ConvertTracesToPprof(traces []collector.Trace, sampleRate int, debug bool) *profile.Profile {
	samplesCount := int64(len(traces))

	if samplesCount == 0 {
		return nil
	}

	// Calculate timing information for the entire batch
	firstSampleTime := traces[0].TsStartProcessing
	lastSampleTime := traces[len(traces)-1].TsEndProcessing
	actualDuration := lastSampleTime.Sub(firstSampleTime)

	// Log batch statistics if debug mode is enabled
	if debug {
		log.Printf("Batch stats: start=%v end=%v duration=%v samples=%d rate=%d/s",
			firstSampleTime.Format(time.RFC3339Nano),
			lastSampleTime.Format(time.RFC3339Nano),
			actualDuration,
			samplesCount,
			sampleRate)
	}

	// Calculate time distribution across samples
	totalCPUTime := actualDuration.Nanoseconds()
	timePerSample := totalCPUTime / samplesCount

	// Initialize the pprof profile with metadata
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "cpu", Unit: "nanoseconds"},
			{Type: "samples", Unit: "count"},
		},
		TimeNanos:     firstSampleTime.UnixNano(),
		DurationNanos: actualDuration.Nanoseconds(),
		PeriodType: &profile.ValueType{
			Type: "cpu",
			Unit: "nanoseconds",
		},
		Period: timePerSample,
	}

	// Maps to track unique functions and locations
	functions := make(map[string]*profile.Function)
	locations := make(map[string]*profile.Location)
	nextFuncID := uint64(1)
	nextLocID := uint64(1)

	// Process each trace and convert to pprof format
	for _, trace := range traces {
		var sampleLocations []*profile.Location

		// Convert each stack frame in the trace
		for _, frame := range trace.Frames {
			functionKey := frame.Method + frame.File

			// Create or reuse function entry
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

			// Create or reuse location entry
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

		// Calculate actual time spent in this trace
		sampleDuration := trace.TsEndProcessing.Sub(trace.TsStartProcessing)
		sampleTime := sampleDuration.Nanoseconds()

		// Add the sample to the profile with its locations, duration, and tags
		prof.Sample = append(prof.Sample, &profile.Sample{
			Location: sampleLocations,
			Value:    []int64{sampleTime, 1},
			Label:    trace.Tags,
		})
	}

	return prof
}
