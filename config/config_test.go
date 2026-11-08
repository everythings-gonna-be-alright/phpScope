package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	c := NewDefault()
	c.PyroscopeURL = "http://pyroscope:4040"
	c.AppName = "my-app"
	c.Tags = map[string]string{"env": "prod", "version": "1.0"}
	return c
}

func TestNewDefault(t *testing.T) {
	c := NewDefault()
	if c.RateHz != 400 || c.Interval != 0.1 || c.BatchLimit != 50000 || c.ConcurrentLimit != 1 {
		t.Errorf("unexpected profiling defaults: %+v", c)
	}
	if c.PhpspyBufferSize != 131072 || c.PhpspyMaxDepth != -1 || c.PhpspyThreads != 64 || c.PhpspyRequestInfo != "p" {
		t.Errorf("unexpected phpspy defaults: %+v", c)
	}
	if c.PhpspyPgrep != `-x "(php-fpm.*|^php$)"` {
		t.Errorf("unexpected pgrep default: %q", c.PhpspyPgrep)
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	for _, url := range []string{"http://pyroscope:4040", "https://profiles.example.com/"} {
		c := validConfig()
		c.PyroscopeURL = url
		c.ExcludePattern = `vendor/.*`
		if err := c.Validate(); err != nil {
			t.Errorf("url %q: unexpected error: %v", url, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing url", func(c *Config) { c.PyroscopeURL = "" }, "pyroscope URL is required"},
		{"relative url", func(c *Config) { c.PyroscopeURL = "pyroscope:4040" }, "absolute http(s) URL"},
		{"non-http scheme", func(c *Config) { c.PyroscopeURL = "file:///etc/passwd" }, "absolute http(s) URL"},
		{"unparseable url", func(c *Config) { c.PyroscopeURL = "http://[::1" }, "absolute http(s) URL"},
		{"missing app name", func(c *Config) { c.AppName = "" }, "app name is required"},
		{"app name with braces", func(c *Config) { c.AppName = "app{x=y}" }, "app name"},
		{"tag key invalid", func(c *Config) { c.Tags = map[string]string{"bad-key": "v"} }, "tag key"},
		{"tag key empty", func(c *Config) { c.Tags = map[string]string{"": "v"} }, "tag key"},
		{"tag value empty", func(c *Config) { c.Tags = map[string]string{"env": ""} }, "value"},
		{"tag value with comma", func(c *Config) { c.Tags = map[string]string{"env": "a,b=c"} }, "value"},
		{"sample label invalid", func(c *Config) { c.SampleLabels = []string{"bad-label"} }, "sample label"},
		{"zero rate", func(c *Config) { c.RateHz = 0 }, "rateHz"},
		{"negative interval", func(c *Config) { c.Interval = -1 }, "interval"},
		{"zero batch", func(c *Config) { c.BatchLimit = 0 }, "batch"},
		{"zero concurrent", func(c *Config) { c.ConcurrentLimit = 0 }, "concurrent"},
		{"bad exclude regex", func(c *Config) { c.ExcludePattern = "(" }, "invalid exclude pattern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	c := &Config{}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"pyroscope URL", "app name", "rateHz", "interval", "batch", "concurrent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}
