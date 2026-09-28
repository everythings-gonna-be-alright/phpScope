package collector

import (
	"phpScope/config"
	"testing"
	"unicode/utf8"
)

func TestParseTraceReplacesInvalidUTF8(t *testing.T) {
	ps := &PhpSpy{config: &config.Config{}}

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

func TestParseTraceKeepsValidTraceUnchanged(t *testing.T) {
	ps := &PhpSpy{config: &config.Config{}}

	trace, _ := ps.parseTrace([]string{"0 App\\Foo::bar /app/a.php:1"})

	if trace.Frames[0].Method != "App\\Foo::bar" {
		t.Fatalf("valid method changed: %q", trace.Frames[0].Method)
	}
}
