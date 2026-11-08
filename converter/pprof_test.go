package converter

import (
	"phpScope/collector"
	"reflect"
	"testing"
	"time"
)

func trace(start time.Time, d time.Duration, tags map[string][]string, frames ...collector.StackFrame) collector.Trace {
	return collector.Trace{
		Frames:            frames,
		Tags:              tags,
		TsStartProcessing: start,
		TsEndProcessing:   start.Add(d),
	}
}

func TestConvertTracesReturnsNilForEmptyInput(t *testing.T) {
	if prof := ConvertTracesToPprof(nil, 400, false); prof != nil {
		t.Fatalf("expected nil for empty traces, got %+v", prof)
	}
}

func TestConvertTracesBuildsValidProfile(t *testing.T) {
	start := time.Unix(1700000000, 0)
	foo := collector.StackFrame{Index: 0, Method: "foo", File: "/a.php", StartLine: 10}
	bar := collector.StackFrame{Index: 1, Method: "bar", File: "/b.php", StartLine: 20}
	tags := map[string][]string{"uri": {"/index.php"}}

	traces := []collector.Trace{
		trace(start, 10*time.Millisecond, tags, foo, bar),
		trace(start.Add(10*time.Millisecond), 30*time.Millisecond, map[string][]string{}, foo),
	}

	prof := ConvertTracesToPprof(traces, 400, true)
	if prof == nil {
		t.Fatal("expected non-nil profile")
	}
	if err := prof.CheckValid(); err != nil {
		t.Fatalf("CheckValid: %v", err)
	}

	if got, want := prof.TimeNanos, start.UnixNano(); got != want {
		t.Errorf("TimeNanos = %d, want %d", got, want)
	}
	if got, want := prof.DurationNanos, (40 * time.Millisecond).Nanoseconds(); got != want {
		t.Errorf("DurationNanos = %d, want %d", got, want)
	}
	if got, want := prof.Period, (20 * time.Millisecond).Nanoseconds(); got != want {
		t.Errorf("Period = %d, want %d", got, want)
	}

	if len(prof.Function) != 2 || len(prof.Location) != 2 {
		t.Fatalf("functions/locations not deduplicated: %d functions, %d locations", len(prof.Function), len(prof.Location))
	}

	if len(prof.Sample) != 2 {
		t.Fatalf("got %d samples, want 2", len(prof.Sample))
	}
	if got, want := prof.Sample[0].Value, []int64{(10 * time.Millisecond).Nanoseconds(), 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("sample 0 values = %v, want %v", got, want)
	}
	if got, want := prof.Sample[1].Value, []int64{(30 * time.Millisecond).Nanoseconds(), 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("sample 1 values = %v, want %v", got, want)
	}
	if len(prof.Sample[0].Location) != 2 || prof.Sample[1].Location[0] != prof.Sample[0].Location[0] {
		t.Errorf("sample locations not shared for identical frames")
	}
	if !reflect.DeepEqual(prof.Sample[0].Label, tags) {
		t.Errorf("labels = %v, want %v", prof.Sample[0].Label, tags)
	}
}

func TestConvertTracesSameFunctionDifferentLines(t *testing.T) {
	start := time.Unix(1700000000, 0)
	traces := []collector.Trace{
		trace(start, time.Millisecond, map[string][]string{},
			collector.StackFrame{Method: "foo", File: "/a.php", StartLine: 1}),
		trace(start, time.Millisecond, map[string][]string{},
			collector.StackFrame{Method: "foo", File: "/a.php", StartLine: 2}),
	}

	prof := ConvertTracesToPprof(traces, 400, false)

	if len(prof.Function) != 1 {
		t.Errorf("got %d functions, want 1", len(prof.Function))
	}
	if len(prof.Location) != 2 {
		t.Errorf("got %d locations, want 2", len(prof.Location))
	}
}

func TestSampleTypeConfigMatchesProfileSampleTypes(t *testing.T) {
	prof := ConvertTracesToPprof([]collector.Trace{trace(time.Now(), time.Millisecond, nil)}, 400, false)
	for _, st := range prof.SampleType {
		cfg, ok := SampleTypeConfig[st.Type]
		if !ok {
			t.Fatalf("sample type %q missing from SampleTypeConfig", st.Type)
		}
		if cfg["units"] != st.Unit {
			t.Errorf("sample type %q units = %v, profile unit = %q", st.Type, cfg["units"], st.Unit)
		}
	}
}
