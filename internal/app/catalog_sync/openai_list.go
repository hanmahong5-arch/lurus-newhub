package catalog_sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// OpenAICompatibleProvider lists models via GET {base}/v1/models.
// Covers OpenAI-compatible gateways including Grok2API, DeepSeek, GLM v4, OpenRouter.
type OpenAICompatibleProvider struct {
	Label      string
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Path       string // default /v1/models
}

func (p *OpenAICompatibleProvider) Name() string {
	if p.Label != "" {
		return p.Label
	}
	return "openai_compatible"
}

func (p *OpenAICompatibleProvider) ListModels(ctx context.Context) ([]UpstreamModel, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	path := p.Path
	if path == "" {
		path = "/v1/models"
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("%s: empty base URL", p.Name())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: GET %s returned %d: %s", p.Name(), path, resp.StatusCode, truncate(string(body), 200))
	}
	var parsed openAIModelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%s: decode models: %w", p.Name(), err)
	}
	out := make([]UpstreamModel, 0, len(parsed.Data))
	seen := map[string]bool{}
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, UpstreamModel{ID: id})
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
