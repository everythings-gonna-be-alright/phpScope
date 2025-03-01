package config

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

	// PHP-specific settings
	PhpspyBufferSize  int
	PhpspyMaxDepth    int
	PhpspyThreads     int
	PhpspyRequestInfo string
}

// NewDefault returns a new default config
func NewDefault() *Config {
	return &Config{
		RateHz:            400,
		Interval:          0.1,
		BatchLimit:        50000,
		ConcurrentLimit:   1,
		PhpspyBufferSize:  131072,
		PhpspyMaxDepth:    -1,
		PhpspyThreads:     64,
		PhpspyRequestInfo: "qcup",
	}
}
