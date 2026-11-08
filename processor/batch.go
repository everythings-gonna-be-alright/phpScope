package processor

import (
	"log"
	"time"

	"phpScope/collector"
	"phpScope/converter"

	"github.com/google/pprof/profile"
)

// processTraces collects traces into batches and triggers processing when either:
// 1. The batch size limit is reached (config.BatchLimit)
// 2. The time interval has elapsed (config.Interval seconds)
func (p *Processor) processTraces(traces <-chan *collector.Trace, pprofProfiles chan<- *profile.Profile) {
	var tracesForProcessing []collector.Trace
	interval := p.interval()

	// Create timer for periodic batch processing
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case trace, ok := <-traces:
			// Channel closed, process remaining traces if any
			if !ok {
				if len(tracesForProcessing) > 0 {
					p.processBatch(tracesForProcessing, pprofProfiles)
				}
				return
			}

			tracesForProcessing = append(tracesForProcessing, *trace)

			// Process batch if size limit reached
			if len(tracesForProcessing) >= p.config.BatchLimit {
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
				timer.Reset(interval)
			}

		case <-timer.C:
			// Process batch on timer expiration if there are traces
			if len(tracesForProcessing) > 0 {
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
			}
			// Add small buffer to interval to avoid exact timing conflicts
			timer.Reset(interval + 100*time.Millisecond)
		}
	}
}

// interval converts the configured fractional seconds into a time.Duration.
func (p *Processor) interval() time.Duration {
	return time.Duration(p.config.Interval * float64(time.Second))
}

// processBatch converts a batch of collected traces into a pprof profile format
// The profile is then sent to the pprofProfiles channel for further processing
func (p *Processor) processBatch(traces []collector.Trace, pprofProfiles chan<- *profile.Profile) {
	if prof := converter.ConvertTracesToPprof(traces, p.config.RateHz, p.config.Debug); prof != nil {
		pprofProfiles <- prof
	}
}

// consumer receives converted pprof profiles from the channel and sends them
// to Pyroscope server using the configured sender
func (p *Processor) consumer(pprofProfiles <-chan *profile.Profile) {
	for prof := range pprofProfiles {
		if err := p.sender.SendSample(prof, converter.SampleTypeConfig); err != nil {
			log.Printf("Error sending sample: %v\n", err)
		}
	}
	log.Println("Sent")
}
