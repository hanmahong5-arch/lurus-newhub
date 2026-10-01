package openai

// The realtime relay runs a client-reader and a target-reader goroutine that
// both touch the usage counters while the handler goroutine reads them at
// teardown. These tests pin the counters' concurrency contract.

import (
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gorilla/websocket"
)

// Both directions are driven at full speed through real websockets. The client
// frames carry zero tokens on purpose: they still write the local counter (what
// the race detector flags) but leave the billed total predictable, so the exact
// sum below also proves no response.done usage was lost or double-counted.
// Run with -race for the full effect; the total check holds without it.
func TestOpenaiRealtimeHandler_ConcurrentDirections_ExactUsageAndNoRace(t *testing.T) {
	const rounds = 300

	clientURL, clientConnCh, closeClientSrv := prov2ndPassOpenaiWsServer(t)
	defer closeClientSrv()
	targetURL, targetConnCh, closeTargetSrv := prov2ndPassOpenaiWsServer(t)
	defer closeTargetSrv()

	testBrowser := prov2ndPassOpenaiDial(t, clientURL)
	defer func() { _ = testBrowser.Close() }()
	testVendor := prov2ndPassOpenaiDial(t, targetURL)
	defer func() { _ = testVendor.Close() }()

	handlerClientConn := <-clientConnCh
	handlerTargetConn := <-targetConnCh
	defer func() { _ = handlerClientConn.Close() }()
	defer func() { _ = handlerTargetConn.Close() }()

	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "model-a"},
		UsePrice:    true, // skips the DB quota lookups
		ClientWs:    handlerClientConn,
		TargetWs:    handlerTargetConn,
	}

	type result struct {
		apiErr *types.NewAPIError
		usage  *dto.RealtimeUsage
	}
	resultCh := make(chan result, 1)
	go func() {
		apiErr, usage := OpenaiRealtimeHandler(w.ctx, info)
		resultCh <- result{apiErr, usage}
	}()

	// The vendor side must keep reading or the forwarded client frames back up.
	go func() {
		for {
			if _, _, err := testVendor.ReadMessage(); err != nil {
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := testBrowser.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{}}`)); err != nil {
				t.Errorf("browser write %d: %v", i, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		done := `{"type":"response.done","response":{"usage":{"total_tokens":3,"input_tokens":2,"output_tokens":1}}}`
		for i := 0; i < rounds; i++ {
			if err := testVendor.WriteMessage(websocket.TextMessage, []byte(done)); err != nil {
				t.Errorf("vendor write %d: %v", i, err)
				return
			}
		}
	}()

	_ = testBrowser.SetReadDeadline(time.Now().Add(20 * time.Second))
	for i := 0; i < rounds; i++ {
		if _, _, err := testBrowser.ReadMessage(); err != nil {
			t.Fatalf("browser read %d/%d: %v", i, rounds, err)
		}
	}
	wg.Wait()

	_ = testBrowser.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = testBrowser.Close()

	select {
	case res := <-resultCh:
		if res.apiErr != nil {
			t.Fatalf("unexpected error: %v", res.apiErr.Error())
		}
		if res.usage == nil {
			t.Fatal("nil usage")
		}
		if res.usage.TotalTokens != 3*rounds || res.usage.InputTokens != 2*rounds || res.usage.OutputTokens != rounds {
			t.Errorf("billed total/in/out = %d/%d/%d, want %d/%d/%d",
				res.usage.TotalTokens, res.usage.InputTokens, res.usage.OutputTokens, 3*rounds, 2*rounds, rounds)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("handler did not return after the client closed")
	}
}

// Deterministic: a fixed number of goroutines hammer every ledger entry point
// and the totals must come out exact (no lost update, no double count).
func TestRealtimeLedger_ConcurrentSettlement_ExactTotals(t *testing.T) {
	const workers, perWorker = 8, 500
	l := &realtimeLedger{}
	upstream := &dto.RealtimeUsage{TotalTokens: 3, InputTokens: 2, OutputTokens: 1}

	var billedTotal, billedMu = 0, sync.Mutex{}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() { // client-reader shape: only feeds the local counter
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				l.addLocalInput(0, 0)
			}
		}()
		go func() { // target-reader shape: settles upstream usage
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				b := l.settleUpstream(upstream)
				billedMu.Lock()
				billedTotal += b.TotalTokens
				billedMu.Unlock()
			}
		}()
	}
	wg.Wait()

	want := workers * perWorker * 3
	if got := l.sumSnapshot().TotalTokens; got != want {
		t.Errorf("sum total = %d, want %d", got, want)
	}
	if billedTotal != want {
		t.Errorf("billed total = %d, want %d (each settlement bills exactly its own usage)", billedTotal, want)
	}
}

func TestRealtimeLedger_LocalTokensBilledOnceAcrossSettleAndDrain(t *testing.T) {
	l := &realtimeLedger{}
	l.addLocalInput(4, 1)
	l.addLocalOutput(2, 3)

	// A usage-less response.done bills the local counter plus its own tokens.
	billed := l.settleLocalInput(10, 0)
	if billed.TotalTokens != 4+1+2+3+10 || billed.InputTokens != 4+1+10 || billed.OutputTokens != 5 {
		t.Errorf("settleLocalInput billed %+v", billed)
	}
	if again := l.drainLocal(); again.TotalTokens != 0 {
		t.Errorf("drainLocal after settle = %+v, want empty (would double bill)", again)
	}

	l.addLocalInput(7, 0)
	if got := l.drainLocal(); got.TotalTokens != 7 {
		t.Errorf("drainLocal = %+v, want 7", got)
	}
	if got := l.sumSnapshot().TotalTokens; got != 20+7 {
		t.Errorf("sum = %d, want 27", got)
	}

	// Upstream usage supersedes (and clears) whatever was counted locally.
	l.addLocalInput(100, 0)
	l.settleUpstream(&dto.RealtimeUsage{TotalTokens: 5})
	if got := l.drainLocal(); got.TotalTokens != 0 {
		t.Errorf("local counter survived an upstream settlement: %+v", got)
	}
}
