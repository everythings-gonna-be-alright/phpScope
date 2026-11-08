package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		in         string
		key, value string
		wantErr    bool
	}{
		{in: "env=prod", key: "env", value: "prod"},
		{in: " env = prod ", key: "env", value: "prod"},
		{in: "query=a=b", key: "query", value: "a=b"},
		{in: "env=", key: "env", value: ""},
		{in: "noequals", wantErr: true},
		{in: "=prod", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tt := range tests {
		key, value, err := parseTag(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseTag(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if key != tt.key || value != tt.value {
			t.Errorf("parseTag(%q) = (%q, %q), want (%q, %q)", tt.in, key, value, tt.key, tt.value)
		}
	}
}

func TestMultiFlag(t *testing.T) {
	var f multiFlag
	_ = f.Set("a=1")
	_ = f.Set("b=2")
	if f.String() != "[a=1 b=2]" {
		t.Fatalf("String() = %q", f.String())
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--pyroscopeUrl=http://pyroscope:4040",
		"--appName=my-app",
		"--auth=token",
		"--tags=env=prod",
		"--tags=version=1.0",
		"--rateHz=100",
		"--interval=0.5",
		"--batch=10",
		"--concurrent=2",
		"--exclude=vendor/",
		"--phpspyBufferSize=1024",
		"--phpspyMaxDepth=50",
		"--phpspyThreads=8",
		"--phpspyRequestInfo=qc",
		"--sampleLabels= path, qstring ,,ts",
		"--pgrep=-x php",
		"--debug",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PyroscopeURL != "http://pyroscope:4040" || cfg.AppName != "my-app" || cfg.AuthToken != "token" {
		t.Errorf("core settings not parsed: %+v", cfg)
	}
	if want := map[string]string{"env": "prod", "version": "1.0"}; !reflect.DeepEqual(cfg.Tags, want) {
		t.Errorf("tags = %v, want %v", cfg.Tags, want)
	}
	if cfg.RateHz != 100 || cfg.Interval != 0.5 || cfg.BatchLimit != 10 || cfg.ConcurrentLimit != 2 || cfg.ExcludePattern != "vendor/" {
		t.Errorf("profiling settings not parsed: %+v", cfg)
	}
	if cfg.PhpspyBufferSize != 1024 || cfg.PhpspyMaxDepth != 50 || cfg.PhpspyThreads != 8 ||
		cfg.PhpspyRequestInfo != "qc" || cfg.PhpspyPgrep != "-x php" || !cfg.Debug {
		t.Errorf("phpspy settings not parsed: %+v", cfg)
	}
	if want := []string{"path", "qstring", "ts"}; !reflect.DeepEqual(cfg.SampleLabels, want) {
		t.Errorf("sample labels = %q, want %q", cfg.SampleLabels, want)
	}
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{"--pyroscopeUrl=http://p:4040", "--appName=a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateHz != 400 || cfg.Interval != 0.1 || len(cfg.Tags) != 0 || cfg.Debug {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.SampleLabels, []string{"path"}) || cfg.PhpspyRequestInfo != "p" {
		t.Errorf("sample labels = %q, request info = %q; want only the script path", cfg.SampleLabels, cfg.PhpspyRequestInfo)
	}
}

func TestParseConfigAuthTokenFromEnv(t *testing.T) {
	base := []string{"--pyroscopeUrl=http://p:4040", "--appName=a"}

	t.Setenv(authTokenEnv, "from-env")
	cfg, err := parseConfig(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "from-env" {
		t.Errorf("token = %q, want the environment value", cfg.AuthToken)
	}

	cfg, err = parseConfig(append(base, "--auth=from-flag"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthToken != "from-flag" {
		t.Errorf("token = %q, want the flag to override the environment", cfg.AuthToken)
	}
}

func TestParseConfigEmptySampleLabels(t *testing.T) {
	cfg, err := parseConfig([]string{"--pyroscopeUrl=http://p:4040", "--appName=a", "--sampleLabels="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.SampleLabels) != 0 {
		t.Errorf("sample labels = %q, want none", cfg.SampleLabels)
	}
}

func TestParseConfigErrors(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	stderr := os.Stderr
	os.Stderr = devNull
	defer func() { os.Stderr = stderr }()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing url", []string{"--appName=a"}, "pyroscope URL is required"},
		{"missing app", []string{"--pyroscopeUrl=http://p:4040"}, "app name is required"},
		{"malformed tag", []string{"--pyroscopeUrl=http://p:4040", "--appName=a", "--tags=novalue"}, "invalid tag"},
		{"invalid tag key", []string{"--pyroscopeUrl=http://p:4040", "--appName=a", "--tags=bad-key=v"}, "tag key"},
		{"non-numeric rate", []string{"--rateHz=fast"}, "invalid value"},
		{"unknown flag", []string{"--nope"}, "not defined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want mention of %q", err, tt.want)
			}
		})
	}

	if _, err := parseConfig([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h error = %v, want flag.ErrHelp", err)
	}
}

func TestPrintWelcomeBanner(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	printWelcomeBanner("http://p:4040", "app", 400, 0.1, 10, 1, map[string]string{"env": "prod"}, []string{"path", "ts"}, "vendor/", "-x php", true)
	os.Stdout = stdout
	_ = w.Close()

	out, _ := io.ReadAll(r)
	for _, want := range []string{"http://p:4040", "app", "env: prod", "path,ts", "vendor/", "-x php"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("banner missing %q", want)
		}
	}
}
