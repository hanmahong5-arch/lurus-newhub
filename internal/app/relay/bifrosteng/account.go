package bifrosteng

import (
	"context"
	"fmt"
	"sync"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
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
	return []schemas.Key{{
		ID:     string(snap.ProviderKey),
		Name:   string(snap.ProviderKey),
		Value:  *schemas.NewEnvVar(snap.APIKey),
		Models: models,
		Weight: snap.Weight,
	}}, nil
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
