package catalog_sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

const officialRatioURLDefault = "https://basellm.github.io/llm-metadata/api/newapi/ratio_config-v1-base.json"

// officialRatioURL is overridden in tests to point at an httptest.
var officialRatioURL = officialRatioURLDefault

// OfficialRatioHints fetches basellm official ratio preset and returns
// model_ratio hints for models present in the upstream list.
func OfficialRatioHints(ctx context.Context, _ *repo.Channel, models []UpstreamModel) ([]PriceHint, error) {
	wanted := map[string]bool{}
	for _, m := range models {
		wanted[m.ID] = true
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, officialRatioURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("official ratios: status %d", resp.StatusCode)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	data, _ := envelope["data"].(map[string]any)
	if data == nil {
		data = envelope
	}
	ratios, _ := data["model_ratio"].(map[string]any)
	if ratios == nil {
		return nil, nil
	}
	out := make([]PriceHint, 0)
	for name, raw := range ratios {
		if !wanted[name] {
			continue
		}
		v, ok := asFloat(raw)
		if !ok {
			continue
		}
		out = append(out, PriceHint{
			ModelName: name,
			Field:     "model_ratio",
			Value:     v,
			Source:    "basellm/ratio_config-v1-base",
		})
	}
	return out, nil
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		var f float64
		_, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}
