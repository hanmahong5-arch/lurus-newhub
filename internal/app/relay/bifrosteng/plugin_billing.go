package bifrosteng

import (
	"github.com/maximhq/bifrost/core/schemas"
)

// billingPlugin is a no-op Bifrost plugin registered so the extension point
// exists. newhub PostConsumeQuota remains the billing source of truth —
// Bifrost budget/virtual-key must never become a second ledger. Usage is
// mapped from BifrostChatResponse in convert.go and settled by TextHelper.
type billingPlugin struct{}

func (p *billingPlugin) GetName() string { return "lurus-newhub-billing-noop" }

func (p *billingPlugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}

func (p *billingPlugin) HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	return nil
}

func (p *billingPlugin) HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

func (p *billingPlugin) PreHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.PluginShortCircuit, error) {
	return req, nil, nil
}

func (p *billingPlugin) PostHook(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return result, err, nil
}

func (p *billingPlugin) Cleanup() error { return nil }
