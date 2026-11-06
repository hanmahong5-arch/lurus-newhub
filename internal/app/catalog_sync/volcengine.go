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

// VolcengineProvider lists Ark models via GET /api/v3/models.
type VolcengineProvider struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func (p *VolcengineProvider) Name() string { return "volcengine" }

func (p *VolcengineProvider) ListModels(ctx context.Context) ([]UpstreamModel, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://ark.cn-beijing.volces.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v3/models", nil)
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
		return nil, fmt.Errorf("volcengine: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("volcengine: decode: %w", err)
	}
	out := make([]UpstreamModel, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		status := strings.TrimSpace(item.Status)
		if status == "Shutdown" {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		out = append(out, UpstreamModel{ID: id})
	}
	return out, nil
}
