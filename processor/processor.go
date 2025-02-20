package processor

import (
	"phpScope/collector"
	"phpScope/config"
	"phpScope/sender"

	"sync"

	"github.com/google/pprof/profile"
)



// New creates a new Processor instance with the given configuration and sender.
// The sender is responsible for transmitting profiles to the Pyroscope server.
func New(cfg *config.Config, sender *sender.Sender) *Processor {
	return &Processor{
		config:     cfg,
		sender:     sender,
		bulkAmount: 0,
	}
}

// Process starts the main processing pipeline:
// 1. Initializes the PHP trace collector
// 2. Starts multiple concurrent trace processors
// 3. Starts a consumer to send profiles to Pyroscope
// Returns an error if the collector fails to start
func (p *Processor) Process() error {
	collector := collector.New(p.config)
	traces, err := collector.Start()
	if err != nil {
		return err
	}

	// Channel for passing converted pprof profiles between processors and consumer
	pprofProfiles := make(chan *profile.Profile, p.config.ConcurrentLimit)
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start multiple trace processors based on ConcurrentLimit
	// Each processor handles batching and converting traces to pprof format
	for i := 0; i < p.config.ConcurrentLimit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.processTraces(traces, pprofProfiles)
		}()
	}

	// Start a single consumer that receives converted profiles
	// and sends them to the Pyroscope server
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(pprofProfiles)
		p.consumer(pprofProfiles)
	}()

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(done)
	}()

	<-done
	return nil
}
