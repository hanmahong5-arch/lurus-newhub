package types

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// relayDoRequestError rebuilds, step for step, the error the non-streaming
// relay path hands to the circuit breaker when client.Do fails:
//
//	provider/api_request.go   types.NewError(err, ErrorCodeDoRequestFailed,
//	                              ErrOptionWithHideErrMsg("upstream error: do request failed"))
//	app/relay/*_handler.go    types.NewOpenAIError(err, ErrorCodeDoRequestFailed, 500)
func relayDoRequestError(transportErr error) *NewAPIError {
	adaptorErr := NewError(transportErr, ErrorCodeDoRequestFailed,
		ErrOptionWithHideErrMsg("upstream error: do request failed"))
	return NewOpenAIError(adaptorErr, ErrorCodeDoRequestFailed, http.StatusInternalServerError)
}

// realTransportError produces the error a real http.Client returns when the
// request context ends mid-flight: the genuine *url.Error chain, not a
// hand-built approximation.
func realTransportError(t *testing.T, mkCtx func() (context.Context, context.CancelFunc), endIt func(cancel context.CancelFunc)) error {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := mkCtx()
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		endIt(cancel)
	}()
	resp, doErr := (&http.Client{}).Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if doErr == nil {
		t.Fatal("client.Do unexpectedly succeeded")
	}
	var ue *url.Error
	if !errors.As(doErr, &ue) {
		t.Fatalf("transport error is %T, want *url.Error", doErr)
	}
	return doErr
}

// A caller that hangs up while the upstream call is in flight is not evidence
// about the channel. Before this fix the hide-message option replaced the
// error with a bare errors.New, severing the context.Canceled chain, so the
// 500 below was read as an upstream failure.
func TestIsUpstreamFailure_ClientHangupDuringDoRequestIsNotAFailure(t *testing.T) {
	doErr := realTransportError(t,
		func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		func(cancel context.CancelFunc) { cancel() })
	if !errors.Is(doErr, context.Canceled) {
		t.Fatalf("setup: transport error does not wrap context.Canceled: %v", doErr)
	}
	e := relayDoRequestError(doErr)
	if IsUpstreamFailure(e) {
		t.Fatalf("client hang-up during client.Do classified as upstream failure (status=%d code=%s err=%v)",
			e.StatusCode, e.GetErrorCode(), e)
	}
	// The wire message must stay the hidden one: no transport detail leaks.
	if got := e.Error(); got != "upstream error: do request failed" {
		t.Fatalf("hidden message changed: %q", got)
	}
}

// The gateway's own deadline (relay client timeout / upstream-call budget) IS
// an upstream failure and must keep counting toward the trip threshold.
func TestIsUpstreamFailure_GatewayOwnDeadlineIsAFailure(t *testing.T) {
	doErr := realTransportError(t,
		func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 30*time.Millisecond)
		},
		func(cancel context.CancelFunc) {})
	if !errors.Is(doErr, context.DeadlineExceeded) {
		t.Fatalf("setup: transport error does not wrap DeadlineExceeded: %v", doErr)
	}
	e := relayDoRequestError(doErr)
	if !IsUpstreamFailure(e) {
		t.Fatalf("gateway-side deadline must be an upstream failure (status=%d code=%s)", e.StatusCode, e.GetErrorCode())
	}
}

// A body read cut short by the caller's hang-up goes through NewOpenAIError with
// a plain error; that constructor used to rebuild Err from the message alone.
func TestIsUpstreamFailure_ClientHangupDuringBodyReadIsNotAFailure(t *testing.T) {
	readErr := fmt.Errorf("read upstream body: %w", context.Canceled)
	e := NewOpenAIError(readErr, ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	if IsUpstreamFailure(e) {
		t.Fatalf("canceled body read classified as upstream failure")
	}
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("NewOpenAIError dropped the cause chain")
	}
	if e.Error() != readErr.Error() {
		t.Fatalf("message changed: %q", e.Error())
	}
}

// channel:* is checked first by IsUpstreamFailure; a channel-scoped error that
// merely wraps the caller's cancellation must not trip the breaker.
func TestIsUpstreamFailure_ChannelErrorWrappingCancelIsNotAFailure(t *testing.T) {
	e := NewError(fmt.Errorf("aws call: %w", context.Canceled), ErrorCodeChannelAwsClientError)
	if !IsChannelError(e) {
		t.Fatal("setup: expected a channel error")
	}
	if IsUpstreamFailure(e) {
		t.Fatalf("channel error wrapping context.Canceled classified as upstream failure")
	}
	// Control: a genuine channel error still is one.
	if !IsUpstreamFailure(NewError(errors.New("bad key"), ErrorCodeChannelInvalidKey)) {
		t.Fatal("control: plain channel error must stay an upstream failure")
	}
}

// Wrapping the cause must not change how the hide option reads or what
// callers compare against.
func TestErrOptionWithHideErrMsg_KeepsCauseButHidesText(t *testing.T) {
	cause := fmt.Errorf("dial tcp 10.0.0.1:443: secret detail: %w", context.DeadlineExceeded)
	e := NewError(cause, ErrorCodeDoRequestFailed, ErrOptionWithHideErrMsg("upstream error: do request failed"))
	if e.Error() != "upstream error: do request failed" {
		t.Fatalf("text leaked: %q", e.Error())
	}
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("cause chain lost")
	}
}
