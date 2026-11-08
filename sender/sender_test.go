package sender

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"phpScope/config"

	"github.com/google/pprof/profile"
)

var sampleTypes = map[string]map[string]interface{}{
	"cpu": {"units": "nanoseconds", "aggregation": "sum"},
}

func validProfile() *profile.Profile {
	fn := &profile.Function{ID: 1, Name: "foo", Filename: "/a.php"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Function:   []*profile.Function{fn},
		Location:   []*profile.Location{loc},
		Sample:     []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{100}}},
	}
}

func newSender(url, token string, tags map[string]string) *Sender {
	s := New(&config.Config{PyroscopeURL: url, AppName: "app", AuthToken: token, Tags: tags, Debug: true})
	s.SetFrom(1)
	s.SetUntil(2)
	return s
}

type captured struct {
	method, path, name, auth string
	prof                     *profile.Profile
	sampleTypes              map[string]map[string]interface{}
}

func captureServer(t *testing.T, status int) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method, c.path = r.Method, r.URL.Path
		c.name = r.URL.Query().Get("name")
		c.auth = r.Header.Get("Authorization")

		pf, _, err := r.FormFile("profile")
		if err != nil {
			t.Errorf("profile part: %v", err)
			return
		}
		defer pf.Close()
		if c.prof, err = profile.Parse(pf); err != nil {
			t.Errorf("profile not parseable: %v", err)
		}

		cf, _, err := r.FormFile("sample_type_config")
		if err != nil {
			t.Errorf("sample_type_config part: %v", err)
			return
		}
		defer cf.Close()
		if err := json.NewDecoder(cf).Decode(&c.sampleTypes); err != nil {
			t.Errorf("sample_type_config not JSON: %v", err)
		}

		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts, c
}

func TestSendSampleSuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts, c := captureServer(t, status)

			if err := newSender(ts.URL, "", nil).SendSample(validProfile(), sampleTypes); err != nil {
				t.Fatalf("SendSample: %v", err)
			}
			if c.method != http.MethodPost || c.path != "/ingest" {
				t.Errorf("request = %s %s, want POST /ingest", c.method, c.path)
			}
			if c.name != "app" {
				t.Errorf("name = %q, want %q", c.name, "app")
			}
			if c.prof == nil || len(c.prof.Sample) != 1 || c.prof.Function[0].Name != "foo" {
				t.Errorf("profile not transmitted intact: %+v", c.prof)
			}
			if !reflect.DeepEqual(c.sampleTypes, sampleTypes) {
				t.Errorf("sample types = %v, want %v", c.sampleTypes, sampleTypes)
			}
		})
	}
}

func TestSendSampleAuthorizationHeader(t *testing.T) {
	tests := map[string]string{"": "", "secret": "Bearer secret"}
	for token, want := range tests {
		ts, c := captureServer(t, http.StatusOK)
		if err := newSender(ts.URL, token, nil).SendSample(validProfile(), sampleTypes); err != nil {
			t.Fatalf("SendSample: %v", err)
		}
		if c.auth != want {
			t.Errorf("token %q: Authorization = %q, want %q", token, c.auth, want)
		}
	}
}

func TestSendSampleAppendsSortedTagsToName(t *testing.T) {
	ts, c := captureServer(t, http.StatusOK)
	tags := map[string]string{"version": "1.0", "env": "prod", "az": "a"}

	if err := newSender(ts.URL, "", tags).SendSample(validProfile(), sampleTypes); err != nil {
		t.Fatalf("SendSample: %v", err)
	}
	if want := "app{az=a,env=prod,version=1.0}"; c.name != want {
		t.Fatalf("name = %q, want %q", c.name, want)
	}
}

func TestSendSampleErrorStatusTruncatesBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("x", 3*maxErrorBodySize))
	}))
	defer ts.Close()

	err := newSender(ts.URL, "", nil).SendSample(validProfile(), sampleTypes)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error lacks status code: %v", err)
	}
	_, body, _ := strings.Cut(err.Error(), "response: ")
	if n := len(body); n != maxErrorBodySize {
		t.Errorf("error body has %d bytes, want %d", n, maxErrorBodySize)
	}
}

func TestSendSampleRejectsInvalidProfileWithoutRequest(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer ts.Close()

	bad := validProfile()
	bad.Sample[0].Value = []int64{1, 2}

	if err := newSender(ts.URL, "", nil).SendSample(bad, sampleTypes); err == nil {
		t.Fatal("expected error for invalid profile")
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid profile was sent to the server")
	}
}

func TestSendSampleConnectionError(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close()

	if err := newSender(url, "", nil).SendSample(validProfile(), sampleTypes); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestSendSampleInvalidURL(t *testing.T) {
	if err := newSender("http://bad host", "", nil).SendSample(validProfile(), sampleTypes); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestSendSampleUnmarshalableSampleTypeConfig(t *testing.T) {
	bad := map[string]map[string]interface{}{"cpu": {"units": make(chan int)}}
	if err := newSender("http://127.0.0.1:1", "", nil).SendSample(validProfile(), bad); err == nil {
		t.Fatal("expected error for unmarshalable sample type config")
	}
}
