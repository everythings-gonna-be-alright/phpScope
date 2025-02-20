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

	"github.com/google/pprof/profile"
)

type Config struct {
	PyroscopeURL string
	AuthToken    string
	AppName      string
	RateHz       int
	Tags         map[string]string
}

type Sender struct {
	config Config
	from   int64
	until  int64
	client *http.Client
}

func New(config Config) *Sender {
	return &Sender{
		config: config,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (s *Sender) SetFrom(from int64) {
	s.from = from
}

func (s *Sender) SetUntil(until int64) {
	s.until = until
}

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

	// jsonBytes, err := json.MarshalIndent(pprofTraces, "", "  ")
    // if err != nil {
    //     return fmt.Errorf("marshalling profile to JSON: %w", err)
	// }
	// log.Printf("Profile as JSON: %s", string(jsonBytes))

	// Convert sampleTypeConfig to JSON
	sampleTypeConfigJSON, err := json.Marshal(sampleTypeConfig)
	if err != nil {
		return fmt.Errorf("marshalling sampleTypeConfig: %w", err)
	}

	// Create a multipart form body
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// Add profile part
	profilePart, err := writer.CreateFormFile("profile", "profile.pprof")
	if err != nil {
		return fmt.Errorf("creating profile part: %w", err)
	}
	if _, err := profilePart.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("writing profile data: %w", err)
	}

	// Add sample_type_config part with config.json field name
	sampleTypeConfigPart, err := writer.CreateFormFile("sample_type_config", "config.json")
	if err != nil {
		return fmt.Errorf("creating sample_type_config part: %w", err)
	}
	if _, err := sampleTypeConfigPart.Write(sampleTypeConfigJSON); err != nil {
		return fmt.Errorf("writing sample_type_config data: %w", err)
	}

	// Close the writer to finalize the multipart form body
	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing writer: %w", err)
	}

	// Set request URL and parameters
	params := url.Values{}
	params.Set("name", s.config.AppName)


	url := fmt.Sprintf("%s/ingest?%s", s.config.PyroscopeURL, params.Encode())

	// Create request
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if s.config.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.AuthToken)
	}

	// Send request
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	// Handle response
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code: %d, response: %s", resp.StatusCode, string(respBody))
	}
	log.Println("Profile sent successfully")

	return nil
}
