package processor

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"phpScope/collector"
	"phpScope/config"
	"phpScope/sender"

	"github.com/google/pprof/profile"
)

// profileStrings returns every string a pprof profile serialises into its string table.
func profileStrings(p *profile.Profile) []string {
	var s []string
	for _, st := range p.SampleType {
		s = append(s, st.Type, st.Unit)
	}
	if p.PeriodType != nil {
		s = append(s, p.PeriodType.Type, p.PeriodType.Unit)
	}
	for _, fn := range p.Function {
		s = append(s, fn.Name, fn.SystemName, fn.Filename)
	}
	for _, m := range p.Mapping {
		s = append(s, m.File, m.BuildID)
	}
	for _, smp := range p.Sample {
		for k, vs := range smp.Label {
			s = append(s, k)
			s = append(s, vs...)
		}
		for k := range smp.NumLabel {
			s = append(s, k)
		}
	}
	s = append(s, p.Comments...)
	return append(s, p.DropFrames, p.KeepFrames)
}

func invalidUTF8Strings(p *profile.Profile) []string {
	var bad []string
	for _, s := range profileStrings(p) {
		if !utf8.ValidString(s) {
			bad = append(bad, s)
		}
	}
	return bad
}

// ingestServer emulates Pyroscope's strictness: it rejects profiles containing
// invalid UTF-8, which is what caused whole batches to be dropped.
type ingestServer struct {
	mu       sync.Mutex
	samples  int
	rejected int
}

func (s *ingestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("profile")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prof, err := profile.Parse(bytes.NewReader(data))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(invalidUTF8Strings(prof)) > 0 {
		s.rejected++
		http.Error(w, "malformed_profile", http.StatusBadRequest)
		return
	}
	s.samples += len(prof.Sample)
	w.WriteHeader(http.StatusOK)
}

func TestIngestServerDetectsInvalidUTF8(t *testing.T) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   []*profile.Function{{ID: 1, Name: "App\\Fo\xff::bar"}},
	}
	var buf bytes.Buffer
	if err := prof.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	parsed, err := profile.Parse(&buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(invalidUTF8Strings(parsed)) == 0 {
		t.Fatal("invalid UTF-8 survived serialisation undetected; the pipeline test below would be meaningless")
	}
}

func TestPipelineInvalidUTF8DoesNotDropBatch(t *testing.T) {
	srv := &ingestServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg := config.NewDefault()
	cfg.PyroscopeURL = ts.URL
	cfg.AppName = "test"
	cfg.BatchLimit = 100
	cfg.Interval = 60
	cfg.SampleLabels = []string{"uri"}

	phpspyOutput := "0 App\\Fo\xff\xfe::bar /app/a\xc3.php:1\n" +
		"1 App\\Main::run /app/index.php:5\n" +
		"# uri = /caf\xe9\n" +
		"\n" +
		"0 App\\Ok::fine /app/ok.php:2\n" +
		"# uri = /ok\n" +
		"\n"

	traces := collector.New(cfg).Read(strings.NewReader(phpspyOutput))
	New(cfg, sender.New(cfg)).run(traces)

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.rejected != 0 {
		t.Fatalf("server rejected %d batches as malformed", srv.rejected)
	}
	if srv.samples != 2 {
		t.Fatalf("server ingested %d samples, want 2", srv.samples)
	}
}
