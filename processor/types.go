package processor

import (
	"phpScope/config"

	"github.com/google/pprof/profile"
)

// profileSender transmits a converted profile to the profiling backend.
type profileSender interface {
	SendSample(prof *profile.Profile, sampleTypeConfig map[string]map[string]interface{}) error
}

// Processor handles the collection and processing of PHP profiling data.
// It manages the lifecycle of trace collection, batching, and sending to Pyroscope.
type Processor struct {
	config *config.Config
	sender profileSender
}
