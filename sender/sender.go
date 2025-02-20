package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"phpScope/config"

	"github.com/google/pprof/profile"
)

// Sender handles sending profiling data to Pyroscope server
type Sender struct {
	config *config.Config
	from   int64        // Start timestamp of the profiling period
	until  int64        // End timestamp of the profiling period
	client *http.Client // HTTP client with timeout configuration
}

// New creates a new Sender instance with the given configuration
func New(cfg *config.Config) *Sender {
	return &Sender{
		config: cfg,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// SetFrom sets the start timestamp for the profiling period
func (s *Sender) SetFrom(from int64) {
	s.from = from
}

// SetUntil sets the end timestamp for the profiling period
func (s *Sender) SetUntil(until int64) {
	s.until = until
}

// SendSample sends a profiling sample to the Pyroscope server.
// It takes a pprof profile and sample type configuration as input.
// The profile data is sent as a multipart form request.
func (s *Sender) SendSample(pprofTraces *profile.Profile, sampleTypeConfig map[string]map[string]interface{}) error {
	// Validate the profile
	if err := pprofTraces.CheckValid(); err != nil {
		return fmt.Errorf("invalid profile: %w", err)
	}

	// Convert the profile data to bytes
	var buf bytes.Buffer
	if err := pprofTraces.Write(&buf); err != nil {
		return fmt.Errorf("writing profile: %w", err)
	}

	// Convert sampleTypeConfig to JSON
	sampleTypeConfigJSON, err := json.Marshal(sampleTypeConfig)
	if err != nil {
		return fmt.Errorf("marshalling sampleTypeConfig: %w", err)
	}

	// Create and send multipart form request
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add profile data to form
	profilePart, err := writer.CreateFormFile("profile", "profile.pprof")
	if err != nil {
		return fmt.Errorf("creating profile part: %w", err)
	}
	if _, err := profilePart.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("writing profile data: %w", err)
	}

	// Add sample type configuration to form
	sampleTypeConfigPart, err := writer.CreateFormFile("sample_type_config", "config.json")
	if err != nil {
		return fmt.Errorf("creating sample_type_config part: %w", err)
	}
	if _, err := sampleTypeConfigPart.Write(sampleTypeConfigJSON); err != nil {
		return fmt.Errorf("writing sample_type_config data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing writer: %w", err)
	}

	// Prepare request URL and parameters
	params := url.Values{}
	params.Set("name", s.config.AppName)
	url := fmt.Sprintf("%s/ingest?%s", s.config.PyroscopeURL, params.Encode())

	// Create and configure HTTP request
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if s.config.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.AuthToken)
	}

	// Send request and handle response
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, string(respBody))
	}

	if s.config.Debug {
		log.Printf("Profile sent successfully for app '%s'. Status: %d, From: %d, Until: %d",
			s.config.AppName, resp.StatusCode, s.from, s.until)
	}

	return nil
}
