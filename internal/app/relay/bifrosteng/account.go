/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package bifrosteng

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/maximhq/bifrost/core/schemas"
)

// channelSnap is an in-memory per-channel view Bifrost Account methods read.
// Hot path must never hit the DB — RegisterChannel is called from the already-
// selected RelayInfo before DispatchChat.
type channelSnap struct {
	ProviderKey  schemas.ModelProvider
	BaseProvider schemas.ModelProvider
	APIKey       string
	BaseURL      string
	Model        string
	Weight       float64
	ApiType      int
	Other        dto.ChannelOtherSettings
	ApiVersion   string // Vertex region / version blob from channel
}

// ChannelAccount implements schemas.Account against the channel snapshot map.
type ChannelAccount struct {
	mu    sync.RWMutex
	snaps map[schemas.ModelProvider]*channelSnap
}

// NewChannelAccount constructs an empty account.
func NewChannelAccount() *ChannelAccount {
	return &ChannelAccount{snaps: make(map[schemas.ModelProvider]*channelSnap)}
}

// RegisterChannel stores (or refreshes) the snapshot for the selected channel.
func (a *ChannelAccount) RegisterChannel(info *relaycommon.RelayInfo) (schemas.ModelProvider, error) {
	if info == nil || info.ChannelMeta == nil {
		return "", fmt.Errorf("bifrosteng: RelayInfo/ChannelMeta required")
	}
	base, ok := baseProvider(info.ApiType)
	if !ok {
		return "", fmt.Errorf("bifrosteng: apiType %d not mapped", info.ApiType)
	}
	key := ProviderKey(info.ChannelId)
	snap := &channelSnap{
		ProviderKey:  key,
		BaseProvider: base,
		APIKey:       info.ApiKey,
		BaseURL:      info.ChannelBaseUrl,
		Model:        info.UpstreamModelName,
		Weight:       1.0,
		ApiType:      info.ApiType,
		Other:        info.ChannelOtherSettings,
		ApiVersion:   info.ApiVersion,
	}
	if snap.Model == "" {
		snap.Model = info.OriginModelName
	}
	a.mu.Lock()
	a.snaps[key] = snap
	a.mu.Unlock()
	return key, nil
}

func (a *ChannelAccount) get(provider schemas.ModelProvider) (*channelSnap, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	snap, ok := a.snaps[provider]
	if !ok {
		return nil, fmt.Errorf("bifrosteng: no snapshot for provider %s", provider)
	}
	return snap, nil
}

// GetConfiguredProviders returns currently registered channel providers.
func (a *ChannelAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]schemas.ModelProvider, 0, len(a.snaps))
	for k := range a.snaps {
		out = append(out, k)
	}
	return out, nil
}

// GetKeysForProvider returns the single channel key from the snapshot.
func (a *ChannelAccount) GetKeysForProvider(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	_ = ctx
	snap, err := a.get(provider)
	if err != nil {
		return nil, err
	}
	models := []string{"*"}
	if snap.Model != "" {
		models = []string{snap.Model, "*"}
	}
	key := schemas.Key{
		ID:     string(snap.ProviderKey),
		Name:   string(snap.ProviderKey),
		Value:  *schemas.NewEnvVar(snap.APIKey),
		Models: models,
		Weight: snap.Weight,
	}
	switch snap.ApiType {
	case constant.APITypeAws:
		if cfg, err := bedrockKeyConfig(snap); err != nil {
			return nil, err
		} else if cfg != nil {
			key.BedrockKeyConfig = cfg
			// Bifrost Bedrock AK/SK auth uses empty Value; API-key mode keeps Value.
			if snap.Other.AwsKeyType != dto.AwsKeyTypeApiKey {
				key.Value = *schemas.NewEnvVar("")
			}
		}
	case constant.APITypeVertexAi:
		if cfg, err := vertexKeyConfig(snap); err != nil {
			return nil, err
		} else if cfg != nil {
			key.VertexKeyConfig = cfg
		}
	}
	return []schemas.Key{key}, nil
}

// GetConfigForProvider returns NetworkConfig with the channel BaseURL and
// CustomProviderConfig pointing at the protocol family. MaxRetries is 0 so
// newhub's outer channel retry remains the only retry layer.
func (a *ChannelAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	snap, err := a.get(provider)
	if err != nil {
		return nil, err
	}
	nc := schemas.DefaultNetworkConfig
	nc.BaseURL = snap.BaseURL
	nc.MaxRetries = 0
	return &schemas.ProviderConfig{
		NetworkConfig:            nc,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: snap.BaseProvider,
		},
	}, nil
}

// bedrockKeyConfig maps newhub's `ak|sk|region` or `apiKey|region` channel key
// into Bifrost's BedrockKeyConfig. Returns nil when the key is not Bedrock-shaped
// so callers can fall through (RegisterChannel already mapped ApiType).
func bedrockKeyConfig(snap *channelSnap) (*schemas.BedrockKeyConfig, error) {
	parts := strings.Split(snap.APIKey, "|")
	switch {
	case snap.Other.AwsKeyType == dto.AwsKeyTypeApiKey && len(parts) == 2:
		return &schemas.BedrockKeyConfig{
			Region: schemas.NewEnvVar(parts[1]),
		}, nil
	case len(parts) == 3:
		return &schemas.BedrockKeyConfig{
			AccessKey: *schemas.NewEnvVar(parts[0]),
			SecretKey: *schemas.NewEnvVar(parts[1]),
			Region:    schemas.NewEnvVar(parts[2]),
		}, nil
	case len(parts) == 2:
		// Default AK/SK layout historically used region as third field; two-part
		// without api_key type is ambiguous — reject so legacy adaptor keeps it.
		return nil, fmt.Errorf("bifrosteng: aws key must be ak|sk|region or apiKey|region")
	default:
		return nil, fmt.Errorf("bifrosteng: invalid aws key shape (%d parts)", len(parts))
	}
}

// vertexKeyConfig maps Vertex JSON service-account key (or api_key mode) into
// Bifrost VertexKeyConfig. Region comes from ApiVersion (channel region field).
func vertexKeyConfig(snap *channelSnap) (*schemas.VertexKeyConfig, error) {
	region := strings.TrimSpace(snap.ApiVersion)
	if region == "" {
		region = "us-central1"
	}
	cfg := &schemas.VertexKeyConfig{
		Region: *schemas.NewEnvVar(region),
	}
	if snap.Other.VertexKeyType == dto.VertexKeyTypeAPIKey {
		// API key auth: ProjectID optional; AuthCredentials empty; Value holds key.
		cfg.ProjectID = *schemas.NewEnvVar("")
		cfg.AuthCredentials = *schemas.NewEnvVar("")
		return cfg, nil
	}
	var sa struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(snap.APIKey), &sa); err != nil || sa.ProjectID == "" {
		return nil, fmt.Errorf("bifrosteng: vertex key must be service-account JSON with project_id")
	}
	cfg.ProjectID = *schemas.NewEnvVar(sa.ProjectID)
	cfg.AuthCredentials = *schemas.NewEnvVar(snap.APIKey)
	return cfg, nil
}
