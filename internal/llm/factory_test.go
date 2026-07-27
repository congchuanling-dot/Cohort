package llm

import (
	"strings"
	"testing"
	"time"
)

func TestNewClientCreatesOpenAIClient_BitsUT(t *testing.T) {
	client, err := NewClient(ProviderConfig{
		ProfileID:      "deepseek",
		Provider:       "openai-compatible",
		Name:           "deepseek",
		APIKey:         "test-key",
		APIBase:        "https://api.deepseek.com",
		Model:          "deepseek-v4-pro",
		Stream:         true,
		ConnectTimeout: 10 * time.Second,
		ReadTimeout:    120 * time.Second,
		MaxRetries:     2,
	})
	if err != nil {
		t.Fatal(err)
	}

	openAI, ok := client.(*OpenAIClient)
	if !ok {
		t.Fatalf("client type = %T, want *OpenAIClient", client)
	}
	if openAI.cfg.Model != "deepseek-v4-pro" {
		t.Fatalf("model = %q, want deepseek-v4-pro", openAI.cfg.Model)
	}
	if openAI.cfg.APIBase != "https://api.deepseek.com" {
		t.Fatalf("api base = %q, want https://api.deepseek.com", openAI.cfg.APIBase)
	}
	if !openAI.cfg.Stream {
		t.Fatal("stream = false, want true")
	}
	if openAI.cfg.MaxRetries != 2 {
		t.Fatalf("max retries = %d, want 2", openAI.cfg.MaxRetries)
	}
}

func TestNewClientRejectsUnknownProvider_BitsUT(t *testing.T) {
	_, err := NewClient(ProviderConfig{
		ProfileID: "claude",
		Provider:  "gemini",
		Name:      "gemini-direct",
	})
	if err == nil {
		t.Fatal("NewClient error = nil, want unsupported provider error")
	}
	for _, want := range []string{`unsupported llm provider "gemini"`, `profile "claude"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want contains %q", err.Error(), want)
		}
	}
}

func TestNewClientCreatesAnthropicClient_BitsUT(t *testing.T) {
	client, err := NewClient(ProviderConfig{
		ProfileID:      "claude",
		Provider:       "anthropic",
		Name:           "claude",
		APIKey:         "test-key",
		APIBase:        "https://api.anthropic.com",
		Model:          "claude-3-5-sonnet-latest",
		Stream:         true,
		ConnectTimeout: 10 * time.Second,
		ReadTimeout:    120 * time.Second,
		MaxRetries:     2,
	})
	if err != nil {
		t.Fatal(err)
	}

	anthropic, ok := client.(*AnthropicClient)
	if !ok {
		t.Fatalf("client type = %T, want *AnthropicClient", client)
	}
	if anthropic.cfg.Model != "claude-3-5-sonnet-latest" {
		t.Fatalf("model = %q, want claude-3-5-sonnet-latest", anthropic.cfg.Model)
	}
	if !anthropic.cfg.Stream {
		t.Fatal("stream = false, want true")
	}
}
