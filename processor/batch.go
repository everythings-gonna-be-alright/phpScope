package processor

import (
	"fmt"
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

	// Create timer for periodic batch processing
	timer := time.NewTimer(time.Duration(p.config.Interval) * time.Second)
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

			// Track number of traces in current batch
			p.mu.Lock()
			p.bulkAmount++
			currentBulkAmount := p.bulkAmount
			p.mu.Unlock()

			// Process batch if size limit reached
			if currentBulkAmount >= p.config.BatchLimit {
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
				p.refreshCounters()
				timer.Reset(time.Duration(p.config.Interval) * time.Second)
			}

		case <-timer.C:
			// Process batch on timer expiration if there are traces
			if len(tracesForProcessing) > 0 {
				p.processBatch(tracesForProcessing, pprofProfiles)
				tracesForProcessing = nil
				p.refreshCounters()
			}
			// Add small buffer to interval to avoid exact timing conflicts
			nextInterval := time.Duration(p.config.Interval)*time.Second + 100*time.Millisecond
			timer.Reset(nextInterval)
		}
	}
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
	for profile := range pprofProfiles {
		if err := p.sender.SendSample(profile, converter.SampleTypeConfig); err != nil {
			fmt.Printf("Error sending sample: %v\n", err)
		}
	}
	log.Println("Sent")
}

// refreshCounters resets the batch counters under mutex protection
// Called after each batch is processed
func (p *Processor) refreshCounters() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bulkAmount = 0
}
