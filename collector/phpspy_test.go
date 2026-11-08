package collector

import (
	"io"
	"os"
	"path/filepath"
	"phpScope/config"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseTraceReplacesInvalidUTF8(t *testing.T) {
	ps := New(&config.Config{SampleLabels: []string{"uri"}})

	trace, err := ps.parseTrace([]string{"0 App\\Fo\xff\xfe::bar /app/a.php:1", "# uri = /caf\xe9"})

	if err != nil {
		t.Fatalf("trace with invalid UTF-8 rejected: %v", err)
	}
	if len(trace.Frames) != 1 || !utf8.ValidString(trace.Frames[0].Method) {
		t.Fatalf("frame not kept as valid UTF-8: %+v", trace.Frames)
	}
	if !utf8.ValidString(trace.Tags["uri"][0]) {
		t.Fatalf("tag not valid UTF-8: %q", trace.Tags["uri"][0])
	}
}

func TestParseTrace(t *testing.T) {
	tests := []struct {
		name    string
		exclude string
		labels  []string
		lines   []string
		frames  []StackFrame
		tags    map[string][]string
	}{
		{
			name:   "valid frame unchanged",
			lines:  []string{"0 App\\Foo::bar /app/a.php:1"},
			frames: []StackFrame{{Index: 0, Method: "App\\Foo::bar", File: "/app/a.php", StartLine: 1}},
			tags:   map[string][]string{},
		},
		{
			name:   "negative line number",
			lines:  []string{"3 <internal> <internal>:-1"},
			frames: []StackFrame{{Index: 3, Method: "<internal>", File: "<internal>", StartLine: -1}},
			tags:   map[string][]string{},
		},
		{
			name:    "exclude pattern filters frame",
			exclude: `vendor/`,
			lines: []string{
				"0 App\\Foo::bar /app/a.php:1",
				"1 Lib\\Baz::qux /app/vendor/lib/Baz.php:10",
			},
			frames: []StackFrame{{Index: 0, Method: "App\\Foo::bar", File: "/app/a.php", StartLine: 1}},
			tags:   map[string][]string{},
		},
		{
			name:    "invalid exclude pattern is ignored",
			exclude: `(`,
			lines:   []string{"0 App\\Foo::bar /app/a.php:1"},
			frames:  []StackFrame{{Index: 0, Method: "App\\Foo::bar", File: "/app/a.php", StartLine: 1}},
			tags:    map[string][]string{},
		},
		{
			name:   "allowed mem and request tags",
			labels: []string{"mem", "uri", "glopeak"},
			lines:  []string{"# mem 1024 2048", "# uri = /index.php", "# glopeak = 5"},
			frames: []StackFrame{},
			tags: map[string][]string{
				"mem":     {"1024 2048"},
				"uri":     {"/index.php"},
				"glopeak": {"5"},
			},
		},
		{
			name:   "tags not in sample labels dropped",
			labels: []string{"path"},
			lines: []string{
				"# mem 1024 2048",
				"# uri = /index.php?token=secret",
				"# path = /var/www/html/index.php",
				"# qstring = token=secret",
				"# cookie = PHPSESSID=abc",
				"# ts = 1794090439.325349",
			},
			frames: []StackFrame{},
			tags:   map[string][]string{"path": {"/var/www/html/index.php"}},
		},
		{
			name:   "unrecognised lines dropped",
			lines:  []string{"garbage", "# no-equals-sign"},
			frames: []StackFrame{},
			tags:   map[string][]string{},
		},
		{
			name:   "empty input",
			lines:  []string{},
			frames: []StackFrame{},
			tags:   map[string][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := New(&config.Config{ExcludePattern: tt.exclude, SampleLabels: tt.labels})

			trace, err := ps.parseTrace(tt.lines)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(trace.Frames, tt.frames) {
				t.Errorf("frames = %+v, want %+v", trace.Frames, tt.frames)
			}
			if !reflect.DeepEqual(trace.Tags, tt.tags) {
				t.Errorf("tags = %+v, want %+v", trace.Tags, tt.tags)
			}
		})
	}
}

func TestReadSplitsTracesOnBlankLines(t *testing.T) {
	input := "0 a /a.php:1\n1 b /b.php:2\n\n0 c /c.php:3\n# uri = /x\n\n"
	ps := New(&config.Config{BatchLimit: 10, SampleLabels: []string{"uri"}})

	var traces []*Trace
	for tr := range ps.Read(strings.NewReader(input)) {
		traces = append(traces, tr)
	}

	if len(traces) != 2 {
		t.Fatalf("got %d traces, want 2", len(traces))
	}
	if len(traces[0].Frames) != 2 || len(traces[1].Frames) != 1 {
		t.Fatalf("unexpected frame counts: %d, %d", len(traces[0].Frames), len(traces[1].Frames))
	}
	if traces[1].Tags["uri"][0] != "/x" {
		t.Fatalf("tag lost: %+v", traces[1].Tags)
	}
	for i, tr := range traces {
		if tr.TsStartProcessing.IsZero() || tr.TsEndProcessing.Before(tr.TsStartProcessing) {
			t.Fatalf("trace %d has invalid timestamps: start=%v end=%v", i, tr.TsStartProcessing, tr.TsEndProcessing)
		}
	}
	if !traces[1].TsStartProcessing.Equal(traces[0].TsEndProcessing) {
		t.Fatalf("timestamps not chained: second start %v != first end %v",
			traces[1].TsStartProcessing, traces[0].TsEndProcessing)
	}
}

func TestReadCapsTraceDurationAfterIdleGap(t *testing.T) {
	const rateHz = 100
	period := time.Second / rateHz
	ps := New(&config.Config{BatchLimit: 10, RateHz: rateHz})

	r, w := io.Pipe()
	traces := ps.Read(r)

	if _, err := io.WriteString(w, "0 a /a.php:1\n\n"); err != nil {
		t.Fatal(err)
	}
	first := <-traces
	time.Sleep(5 * period)
	if _, err := io.WriteString(w, "0 b /b.php:2\n\n"); err != nil {
		t.Fatal(err)
	}
	second := <-traces
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if got := second.TsEndProcessing.Sub(second.TsStartProcessing); got != period {
		t.Fatalf("trace after idle gap covers %v, want one sampling period %v", got, period)
	}
	if !second.TsStartProcessing.After(first.TsEndProcessing) {
		t.Fatalf("trace after idle gap starts at %v, before the previous trace ended at %v",
			second.TsStartProcessing, first.TsEndProcessing)
	}
}

func TestReadHandlesLinesLongerThanDefaultScannerBuffer(t *testing.T) {
	long := "0 " + strings.Repeat("x", 100*1024) + " /a.php:1\n\n"
	ps := New(&config.Config{BatchLimit: 1})

	var traces []*Trace
	for tr := range ps.Read(strings.NewReader(long)) {
		traces = append(traces, tr)
	}

	if len(traces) != 1 || len(traces[0].Frames) != 1 {
		t.Fatalf("long line not parsed: %d traces", len(traces))
	}
}

func TestPhpspyArgsAreNotShellInterpreted(t *testing.T) {
	cfg := config.NewDefault()
	cfg.PhpspyRequestInfo = "qcup; touch /tmp/pwned"
	ps := New(cfg)

	want := []string{
		"--rate-hz=400",
		`--pgrep=-x "(php-fpm.*|^php$)"`,
		"--buffer-size=131072",
		"--max-depth=-1",
		"--threads=64",
		"--request-info=qcup; touch /tmp/pwned",
	}
	if got := ps.phpspyArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestStartExecsPhpspyWithoutShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake phpspy is a POSIX shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	pwned := filepath.Join(dir, "pwned")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argsFile + "'\n" +
		"printf '0 App\\\\Foo::bar /app/a.php:1\\n\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "phpspy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.NewDefault()
	cfg.BatchLimit = 1
	cfg.PhpspyRequestInfo = "qcup; touch " + pwned
	traces, err := New(cfg).Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got []*Trace
	for tr := range traces {
		got = append(got, tr)
	}
	if len(got) != 1 || got[0].Frames[0].Method != "App\\Foo::bar" {
		t.Fatalf("unexpected traces: %+v", got)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--request-info=qcup; touch "+pwned+"\n") {
		t.Fatalf("request-info not passed as a single argument:\n%s", args)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("config value was executed by a shell")
	}
}

func TestStartFailsWhenPhpspyMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := New(config.NewDefault()).Start(); err == nil {
		t.Fatal("expected error when phpspy is not installed")
	}
}

func FuzzParseTrace(f *testing.F) {
	f.Add("0 App\\Foo::bar /app/a.php:1\n# uri = /index.php\n# mem 1 2")
	f.Add("0 App\\Fo\xff\xfe::bar /app/a.php:1\n# uri = /caf\xe9")
	f.Add("1 \xc3 \xe2\x82:7")
	f.Add("")

	ps := New(&config.Config{SampleLabels: []string{"uri", "path", "qstring", "cookie", "ts", "mem"}})
	f.Fuzz(func(t *testing.T, data string) {
		trace, err := ps.parseTrace(strings.Split(data, "\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, fr := range trace.Frames {
			if !utf8.ValidString(fr.Method) || !utf8.ValidString(fr.File) {
				t.Fatalf("invalid UTF-8 in frame: %+v", fr)
			}
		}
		for k, vs := range trace.Tags {
			if !utf8.ValidString(k) {
				t.Fatalf("invalid UTF-8 in tag key %q", k)
			}
			for _, v := range vs {
				if !utf8.ValidString(v) {
					t.Fatalf("invalid UTF-8 in tag %q value %q", k, v)
				}
			}
		}
	})
}
