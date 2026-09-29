package handler

// model_probe_wiring.go wires the active per-model prober
// (internal/app/modelprobe) on top of this package's own channel probe
// (channel-test.go's probeChannel machinery) and starts its leader-gated
// loop. Kept in its own file, not channel-test.go: the prober's ProbeFunc
// only calls unexported functions/types that already live there
// (probeChannel, channelProbeOptions), and modelprobe itself must not import
// this package (layering — see internal/app/modelprobe's package doc), so
// the adapter has to live on this side of the boundary.

import (
	"context"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/modelprobe"
)

// modelProbeLoopTick is the production tick interval for StartModelProber;
// modelprobe.Loop's own tests pass a short duration of their own.
const modelProbeLoopTick = 30 * time.Minute

// modelProbeFunc adapts probeChannel to modelprobe.ProbeFunc: the same probe
// machinery the manual "test channel" button and the automatic channel-level
// pass use, with RecordConsumeLog off — this is a background probe, not an
// operator action, so it must not write a consume-log row. It also carries
// none of the channel-level auto-ban/auto-enable side effects
// autoProbeChannel applies (evaluateProbeOutcome, processChannelError,
// app.EnableChannel): the model prober's own decision layer
// (modelprobe.Apply) is keyed on (channel, model), not the channel alone,
// and owns disable/recover entirely on its own.
//
// probeChannel does not accept an external context — it builds its own
// channelProbeTimeout() deadline internally — so ctx is not threaded into
// the HTTP call here. The existing automatic channel-level pass
// (autoProbeChannel) has the same limitation.
func modelProbeFunc(ctx context.Context, channel *repo.Channel, model string) modelprobe.Result {
	tik := time.Now()
	//nolint:contextcheck // probeChannel takes no context and builds its own timeout (same call shape as autoProbeChannel)
	result := probeChannel(channel, model, "", channelProbeOptions{RecordConsumeLog: false})
	elapsed := time.Since(tik)

	res := modelprobe.Result{
		OK:        result.localErr == nil && result.newAPIError == nil,
		LatencyMs: int(elapsed.Milliseconds()),
	}
	switch {
	case result.newAPIError != nil:
		res.StatusCode = result.newAPIError.StatusCode
		res.Err = result.newAPIError.Error()
	case result.localErr != nil:
		res.Err = result.localErr.Error()
	}
	return res
}

// StartModelProber starts the leader-gated per-model health probe loop
// (internal/app/modelprobe.Loop) with the real ProbeFunc above. It returns
// when ctx is done; cmd/server/main.go runs it in its own errgroup goroutine
// next to the channel-level "automatically test channels" task.
func StartModelProber(ctx context.Context) {
	modelprobe.Loop(ctx, modelProbeFunc, modelProbeLoopTick)
}
