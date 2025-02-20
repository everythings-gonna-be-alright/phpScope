package collector

import (
	"phpScope/config"
	"time"
)

type PhpSpy struct {
	config  *config.Config
	traces  chan *Trace
	stopped chan struct{}
}

// Trace represents a complete PHP stack trace with metadata
type Trace struct {
	Frames            []StackFrame        // Stack frames in the trace
	Tags              map[string][]string // Metadata tags for the trace
	TsStartProcessing time.Time           // Start time of trace processing
	TsEndProcessing   time.Time           // End time of trace processing
}

// StackFrame represents a single frame in a PHP stack trace
type StackFrame struct {
	Index     int    // Frame position in the stack (0 is most recent)
	Method    string // Name of the PHP function/method
	File      string // Source file path
	StartLine int    // Line number in the source file
}
