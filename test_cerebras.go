package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
)

type loggingRoundTripper struct{}

func (rt *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBody, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
	fmt.Printf("--- REQUEST ---\n%s\n\n", reqBody)

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewBuffer(respBody))
	fmt.Printf("--- RESPONSE ---\nStatus: %d\n%s\n\n", resp.StatusCode, respBody)

	return resp, nil
}

// Mock configuration.Secret implementation for the test
type mockSecret struct {
	value []byte
}
func (s *mockSecret) GetSecret(ctx context.Context, name string) ([]byte, error) {
	if val := os.Getenv(name); val != "" {
		return []byte(val), nil
	}
	return []byte("fake_key"), nil
}
func (s *mockSecret) SaveSecret(ctx context.Context, name string, value []byte) error {
	return nil
}

type ExtractedQuery struct {
	Text string `json:"text"`
}

type extractedFactLLM struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

type decompositionOutput struct {
	Facts    []extractedFactLLM `json:"facts"`
	Queries  []ExtractedQuery   `json:"queries"`
	Entities []string           `json:"entities"`
}

func (o *decompositionOutput) SchemaDescription() string {
	return "Extracted facts and search queries"
}
func (o *decompositionOutput) Validate() error { return nil }
func (o *decompositionOutput) Unmarshal(data []byte) error { return nil }

func main() {
	http.DefaultClient.Transport = &loggingRoundTripper{}
	
	os.Setenv("CEREBRAS_API_KEY", "csk-edxcewrn6ef555dhycwetje9txyj5cf664h4k4pjp4298xpv")
	
	registry, err := bwai.NewModelRegistry("models.json", "prompts", &mockSecret{})
	if err != nil {
		panic(err)
	}

	cli := bwaiclient.NewBWAIClient(registry, nil, nil)
	
	out := &decompositionOutput{}
	err = cli.ExecuteAs(context.Background(), [16]byte{}, "decompose_recall", "cerebras-llama3.1-8b", &bwai.PromptData{
		Data: map[string]interface{}{"Content": "Caroline researched", "EventDate": "2026-05-13", "UserName": "Caroline"},
		Messages: []bwai.PromptMessage{},
	}, out)
	
	if err != nil {
		fmt.Printf("ERROR: %+v\n", err)
		
		// If it's an APIError from OpenAI, let's try to dig into it if we can, but since the error is wrapped by bwai we need to use errors.As
		// Wait, let's just use JSON or print the raw HTTP request/response by creating a custom RoundTripper
	} else {
		fmt.Printf("SUCCESS\n")
	}
}