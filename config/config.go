package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var tagKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Characters that would break the Pyroscope "app{k=v,...}" name format.
const reservedNameChars = "{},="

// Config contains all the configuration for the application
type Config struct {
	// Core settings
	PyroscopeURL string
	AuthToken    string
	AppName      string
	Tags         map[string]string
	Debug        bool

	// Profiling settings
	RateHz          int
	Interval        float64
	BatchLimit      int
	ConcurrentLimit int
	ExcludePattern  string

	// SampleLabels lists the phpspy trace fields (uri, path, qstring, cookie,
	// ts, mem) attached to every sample. Each distinct value becomes a separate
	// Pyroscope series.
	SampleLabels []string

	// PHP-specific settings
	PhpspyBufferSize  int
	PhpspyMaxDepth    int
	PhpspyThreads     int
	PhpspyRequestInfo string
	PhpspyPgrep       string
}

// NewDefault returns a new default config
func NewDefault() *Config {
	return &Config{
		RateHz:            400,
		Interval:          0.1,
		BatchLimit:        50000,
		ConcurrentLimit:   1,
		SampleLabels:      []string{"path", "uri"},
		PhpspyBufferSize:  131072,
		PhpspyMaxDepth:    -1,
		PhpspyThreads:     64,
		PhpspyRequestInfo: "pu",
		PhpspyPgrep:       `-x "(php-fpm.*|^php$)"`,
	}
}

// Validate checks that the configuration is usable and returns all problems found.
func (c *Config) Validate() error {
	var errs []error

	if c.PyroscopeURL == "" {
		errs = append(errs, errors.New("pyroscope URL is required"))
	} else if u, err := url.Parse(c.PyroscopeURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("pyroscope URL %q must be an absolute http(s) URL", c.PyroscopeURL))
	}

	if c.AppName == "" {
		errs = append(errs, errors.New("app name is required"))
	} else if strings.ContainsAny(c.AppName, reservedNameChars) {
		errs = append(errs, fmt.Errorf("app name %q must not contain any of %q", c.AppName, reservedNameChars))
	}

	for k, v := range c.Tags {
		if !tagKeyRegex.MatchString(k) {
			errs = append(errs, fmt.Errorf("tag key %q must match %s", k, tagKeyRegex))
		}
		if v == "" || strings.ContainsAny(v, reservedNameChars) {
			errs = append(errs, fmt.Errorf("tag %q value %q must be non-empty and must not contain any of %q", k, v, reservedNameChars))
		}
	}

	for _, l := range c.SampleLabels {
		if !tagKeyRegex.MatchString(l) {
			errs = append(errs, fmt.Errorf("sample label %q must match %s", l, tagKeyRegex))
		}
	}

	if c.RateHz <= 0 {
		errs = append(errs, fmt.Errorf("rateHz must be positive, got %d", c.RateHz))
	}
	if c.Interval <= 0 {
		errs = append(errs, fmt.Errorf("interval must be positive, got %v", c.Interval))
	}
	if c.BatchLimit <= 0 {
		errs = append(errs, fmt.Errorf("batch must be positive, got %d", c.BatchLimit))
	}
	if c.ConcurrentLimit <= 0 {
		errs = append(errs, fmt.Errorf("concurrent must be positive, got %d", c.ConcurrentLimit))
	}

	if c.ExcludePattern != "" {
		if _, err := regexp.Compile(c.ExcludePattern); err != nil {
			errs = append(errs, fmt.Errorf("invalid exclude pattern: %w", err))
		}
	}

	return errors.Join(errs...)
}
