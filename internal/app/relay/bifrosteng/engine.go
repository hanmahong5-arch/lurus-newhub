package bifrosteng

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

var (
	mu      sync.RWMutex
	client  *bifrost.Bifrost
	account *ChannelAccount
)

// Init starts Bifrost when RELAY_ENGINE=bifrost. No-op when legacy.
func Init(ctx context.Context) error {
	if !Enabled() {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	if client != nil {
		return nil
	}
	account = NewChannelAccount()
	c, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account: account,
		Plugins: []schemas.Plugin{&billingPlugin{}},
	})
	if err != nil {
		account = nil
		return fmt.Errorf("bifrosteng: init: %w", err)
	}
	client = c
	return nil
}

// Shutdown tears down Bifrost. Safe when never Init'd.
func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if client != nil {
		client.Shutdown()
		client = nil
	}
	account = nil
}

// Client returns the live Bifrost instance, or nil when disabled / not Init'd.
func Client() *bifrost.Bifrost {
	mu.RLock()
	defer mu.RUnlock()
	return client
}

// Account returns the ChannelAccount, or nil.
func Account() *ChannelAccount {
	mu.RLock()
	defer mu.RUnlock()
	return account
}

// Ready is true when Bifrost is enabled and Init succeeded.
func Ready() bool {
	return Enabled() && Client() != nil && Account() != nil
}

// ShouldHandleChat is the TextHelper gate: engine on, chat mode, mapped
// apiType, client ready, no pass-through, and a convertible request.
// Unmapped / unsupported requests fall through to the legacy adaptor.
func ShouldHandleChat(apiType int, req *dto.GeneralOpenAIRequest, passThrough bool) bool {
	if !Ready() || !ModeEnabled(ModeChat) || !IsMapped(apiType) || passThrough {
		return false
	}
	return ChatConvertible(req)
}

// Snapshot is the admin-console view of this replica's relay engine.
// Per-replica by design (same honesty contract as gateway/health breakers).
func Snapshot() map[string]any {
	modeList := make([]string, 0, len(Modes()))
	for m := range Modes() {
		modeList = append(modeList, m)
	}
	sort.Strings(modeList)
	return map[string]any{
		"engine":         Engine(),
		"modes":          modeList,
		"ready":          Ready(),
		"replica_scoped": true,
		"totals":         EngineTotals(),
		// Historical /metrics series remain the long-window source of truth;
		// totals here are since this process started.
		"since": "process_start",
	}
}
