package openai

import (
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// realtimeLedger holds the usage counters of a realtime session. The
// client-reader and target-reader goroutines both feed them while the handler
// goroutine reads them at teardown, so every access goes through mu. Billing
// (PreWssConsumeQuota, a DB call) runs on the returned snapshots, never while
// holding the lock.
type realtimeLedger struct {
	mu    sync.Mutex
	local dto.RealtimeUsage // locally counted tokens not yet billed
	sum   dto.RealtimeUsage // everything billed so far
}

func (l *realtimeLedger) addLocalInput(text, audio int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	addLocalInput(&l.local, text, audio)
}

func (l *realtimeLedger) addLocalOutput(text, audio int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.local.TotalTokens += text + audio
	l.local.OutputTokens += text + audio
	l.local.OutputTokenDetails.TextTokens += text
	l.local.OutputTokenDetails.AudioTokens += audio
}

// settleUpstream adds an upstream response.done usage to the running sum and
// returns the amount to bill. The locally counted tokens are dropped: the
// upstream figure supersedes them.
func (l *realtimeLedger) settleUpstream(u *dto.RealtimeUsage) dto.RealtimeUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	billed := *u
	addRealtimeUsage(&l.sum, &billed)
	l.local = dto.RealtimeUsage{}
	return billed
}

// settleLocalInput is the fallback for a response.done without usage: the
// locally counted tokens plus this event's own are billed instead.
func (l *realtimeLedger) settleLocalInput(text, audio int) dto.RealtimeUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	addLocalInput(&l.local, text, audio)
	billed := l.local
	addRealtimeUsage(&l.sum, &billed)
	l.local = dto.RealtimeUsage{}
	return billed
}

// drainLocal takes the locally counted tokens not yet billed into the sum.
func (l *realtimeLedger) drainLocal() dto.RealtimeUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	local := l.local
	addRealtimeUsage(&l.sum, &local)
	l.local = dto.RealtimeUsage{}
	return local
}

func (l *realtimeLedger) sumSnapshot() dto.RealtimeUsage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sum
}

func addLocalInput(dst *dto.RealtimeUsage, text, audio int) {
	dst.TotalTokens += text + audio
	dst.InputTokens += text + audio
	dst.InputTokenDetails.TextTokens += text
	dst.InputTokenDetails.AudioTokens += audio
}

func addRealtimeUsage(dst, src *dto.RealtimeUsage) {
	dst.TotalTokens += src.TotalTokens
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.InputTokenDetails.CachedTokens += src.InputTokenDetails.CachedTokens
	dst.InputTokenDetails.TextTokens += src.InputTokenDetails.TextTokens
	dst.InputTokenDetails.AudioTokens += src.InputTokenDetails.AudioTokens
	dst.OutputTokenDetails.TextTokens += src.OutputTokenDetails.TextTokens
	dst.OutputTokenDetails.AudioTokens += src.OutputTokenDetails.AudioTokens
}
