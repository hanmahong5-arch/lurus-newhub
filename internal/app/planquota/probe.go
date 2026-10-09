package planquota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	probeTimeout  = 15 * time.Second
	probeMaxBytes = 256 * 1024
)

// ErrAuth means the vendor rejected the account key on the quota endpoint.
var ErrAuth = errors.New("quota endpoint rejected the account key")

// QuotaURL maps a plan kind and the channel base URL to the vendor's quota
// endpoint. Hosts are fixed per vendor (never taken verbatim from the channel)
// so a mistyped base URL cannot turn the probe into an arbitrary fetch.
// Endpoints adapted from sub2api cn_provider_quota_service.go (LGPL-3.0).
func QuotaURL(kind, baseURL string) (string, error) {
	low := strings.ToLower(baseURL)
	switch kind {
	case KindZhipuCoding:
		host := "https://open.bigmodel.cn"
		if strings.Contains(low, "z.ai") && !strings.Contains(low, "bigmodel.cn") {
			host = "https://api.z.ai"
		}
		return host + "/api/monitor/usage/quota/limit", nil
	case KindKimiCoding:
		return "https://api.kimi.com/coding/v1/usages", nil
	case KindMiniMax:
		if strings.Contains(low, "minimax.io") {
			return "https://api.minimax.io/v1/api/openplatform/coding_plan/remains", nil
		}
		return "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains", nil
	}
	return "", fmt.Errorf("unsupported plan kind %q", kind)
}

// Fetch queries the quota endpoint read-only with the account's own key and
// parses the windows.
func Fetch(ctx context.Context, client *http.Client, kind, baseURL, key string) ([]Window, error) {
	target, err := QuotaURL(kind, baseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if kind == KindZhipuCoding {
		req.Header.Set("Authorization", key) // the zhipu endpoint takes the bare key
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeMaxBytes))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrAuth, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("quota endpoint HTTP %d", resp.StatusCode)
	}
	return Parse(kind, body)
}
