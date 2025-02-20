package processor

import (
	"phpScope/config"
	"phpScope/sender"

	"sync"
)

// Processor handles the collection and processing of PHP profiling data.
// It manages the lifecycle of trace collection, batching, and sending to Pyroscope.
type Processor struct {
	config     *config.Config
	sender     *sender.Sender
	bulkAmount int        // Current number of traces in the batch
	mu         sync.Mutex // Mutex for thread-safe counter updates
}