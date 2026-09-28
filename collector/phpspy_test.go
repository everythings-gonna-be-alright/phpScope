package collector

import (
	"phpScope/config"
	"testing"
)

func TestParseTraceDropsInvalidUTF8(t *testing.T) {
	ps := &PhpSpy{config: &config.Config{}}
	if _, err := ps.parseTrace([]string{"0 App\\Foo::bar /app/a.php:1"}); err != nil {
		t.Fatalf("valid trace rejected: %v", err)
	}
	if _, err := ps.parseTrace([]string{"0 App\\Fo\xff\xfe::bar /app/a.php:1"}); err == nil {
		t.Fatal("invalid UTF-8 trace accepted")
	}
}
