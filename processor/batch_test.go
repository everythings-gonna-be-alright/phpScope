package processor

import (
	"errors"
	"phpScope/collector"
	"phpScope/config"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

type fakeSender struct {
	mu       sync.Mutex
	profiles []*profile.Profile
	failNext int
}

func (f *fakeSender) SendSample(prof *profile.Profile, _ map[string]map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles = append(f.profiles, prof)
	if f.failNext > 0 {
		f.failNext--
		return errors.New("backend unavailable")
	}
	return nil
}

func (f *fakeSender) samples() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.profiles {
		n += len(p.Sample)
	}
	return n
}

func testTrace() *collector.Trace {
	now := time.Now()
	return &collector.Trace{
		Frames:            []collector.StackFrame{{Method: "foo", File: "/a.php", StartLine: 1}},
		Tags:              map[string][]string{},
		TsStartProcessing: now,
		TsEndProcessing:   now.Add(time.Millisecond),
	}
}

func testConfig(batch int, interval float64, concurrent int) *config.Config {
	cfg := config.NewDefault()
	cfg.BatchLimit = batch
	cfg.Interval = interval
	cfg.ConcurrentLimit = concurrent
	return cfg
}

func TestIntervalKeepsFractionalSeconds(t *testing.T) {
	tests := map[float64]time.Duration{
		0.1: 100 * time.Millisecond,
		0.5: 500 * time.Millisecond,
		1.5: 1500 * time.Millisecond,
		2:   2 * time.Second,
	}
	for in, want := range tests {
		p := New(testConfig(1, in, 1), &fakeSender{})
		if got := p.interval(); got != want {
			t.Errorf("interval(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestProcessTracesFlushesAtBatchLimit(t *testing.T) {
	p := New(testConfig(2, 60, 1), &fakeSender{})
	traces := make(chan *collector.Trace, 4)
	out := make(chan *profile.Profile, 4)
	for i := 0; i < 4; i++ {
		traces <- testTrace()
	}
	close(traces)

	p.processTraces(traces, out)
	close(out)

	var sizes []int
	for prof := range out {
		sizes = append(sizes, len(prof.Sample))
	}
	if len(sizes) != 2 || sizes[0] != 2 || sizes[1] != 2 {
		t.Fatalf("batch sizes = %v, want [2 2]", sizes)
	}
}

func TestProcessTracesFlushesRemainderOnClose(t *testing.T) {
	p := New(testConfig(10, 60, 1), &fakeSender{})
	traces := make(chan *collector.Trace, 3)
	out := make(chan *profile.Profile, 1)
	for i := 0; i < 3; i++ {
		traces <- testTrace()
	}
	close(traces)

	p.processTraces(traces, out)

	select {
	case prof := <-out:
		if len(prof.Sample) != 3 {
			t.Fatalf("got %d samples, want 3", len(prof.Sample))
		}
	default:
		t.Fatal("remaining traces not flushed on close")
	}
}

func TestProcessTracesFlushesOnTimerNotBefore(t *testing.T) {
	p := New(testConfig(1000, 0.5, 1), &fakeSender{})
	traces := make(chan *collector.Trace)
	out := make(chan *profile.Profile, 1)
	go p.processTraces(traces, out)
	defer close(traces)

	traces <- testTrace()

	select {
	case <-out:
		t.Fatal("batch flushed before the configured 0.5s interval")
	case <-time.After(250 * time.Millisecond):
	}

	select {
	case prof := <-out:
		if len(prof.Sample) != 1 {
			t.Fatalf("got %d samples, want 1", len(prof.Sample))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("batch not flushed on timer")
	}
}

func TestConsumerContinuesAfterSendError(t *testing.T) {
	fs := &fakeSender{failNext: 1}
	p := New(testConfig(1, 60, 1), fs)
	in := make(chan *profile.Profile, 2)
	in <- &profile.Profile{}
	in <- &profile.Profile{}
	close(in)

	p.consumer(in)

	if len(fs.profiles) != 2 {
		t.Fatalf("sender called %d times, want 2", len(fs.profiles))
	}
}

func TestRunDeliversAllTracesAndReturnsWhenInputCloses(t *testing.T) {
	fs := &fakeSender{}
	p := New(testConfig(5, 60, 3), fs)
	traces := make(chan *collector.Trace, 23)
	for i := 0; i < 23; i++ {
		traces <- testTrace()
	}
	close(traces)

	done := make(chan struct{})
	go func() {
		p.run(traces)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the traces channel closed")
	}
	if got := fs.samples(); got != 23 {
		t.Fatalf("delivered %d samples, want 23", got)
	}
}
