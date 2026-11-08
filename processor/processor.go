package processor

import (
	"phpScope/collector"
	"phpScope/config"

	"sync"

	"github.com/google/pprof/profile"
)

// New creates a new Processor instance with the given configuration and sender.
// The sender is responsible for transmitting profiles to the Pyroscope server.
func New(cfg *config.Config, sender profileSender) *Processor {
	return &Processor{
		config: cfg,
		sender: sender,
	}
}

// Process starts the main processing pipeline:
// 1. Initializes the PHP trace collector
// 2. Starts multiple concurrent trace processors
// 3. Starts a consumer to send profiles to Pyroscope
// Returns an error if the collector fails to start
func (p *Processor) Process() error {
	traces, err := collector.New(p.config).Start()
	if err != nil {
		return err
	}
	p.run(traces)
	return nil
}

// run batches traces into profiles and sends them until traces is closed and
// every pending profile has been handed to the sender.
func (p *Processor) run(traces <-chan *collector.Trace) {
	// Channel for passing converted pprof profiles between processors and consumer
	pprofProfiles := make(chan *profile.Profile, p.config.ConcurrentLimit)

	// Start multiple trace processors based on ConcurrentLimit
	// Each processor handles batching and converting traces to pprof format
	var producers sync.WaitGroup
	for i := 0; i < p.config.ConcurrentLimit; i++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			p.processTraces(traces, pprofProfiles)
		}()
	}

	// Start a single consumer that receives converted profiles
	// and sends them to the Pyroscope server
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		p.consumer(pprofProfiles)
	}()

	producers.Wait()
	close(pprofProfiles)
	<-consumerDone
}
