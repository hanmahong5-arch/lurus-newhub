package handler

// breaker_view.go - the seam that lets the fallback routing strategy read the
// per-channel circuit breakers. The registry (channelBreakers, relay.go) lives
// in this package and middleware cannot import it, so the view is registered
// into routingfallback at init, the same direction every other handler-owned
// capability reaches the lower layers.
//
// Registry.AllOpen is a read-only view (it never creates a breaker and never
// consumes a half-open probe slot), so consulting it on the request path of an
// opted-in tenant cannot change what the relay's own Allow() sees.

import "github.com/LurusTech/lurus-hub/internal/app/routingfallback"

func init() {
	routingfallback.BreakerAllOpen = channelBreakers.AllOpen
}
