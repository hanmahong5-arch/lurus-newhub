package app

// convert_usage_parity_test.go — the terminal usage a Claude-wire client
// receives must not depend on whether it asked for a stream.
//
// This is the forcing function for the defect class behind P2-1: the same
// OpenAI->Claude usage was built by three hand-written literals (two identical
// streaming copies plus a non-streaming one), and the non-streaming copy simply
// never had the cache fields. Nothing connected them, so the streaming tests
// stayed green while every non-streamed /v1/messages reply reported
// cache_read_input_tokens=0 on a request that was billed at the cache discount.
//
// The parity assertion below is field-count aware: adding a field to
// dto.ClaudeUsage without teaching claudeTerminalUsage about it leaves the new
// field zero on BOTH paths, so parity alone would not notice. The explicit
// field census catches that half.

import (
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// fullyPopulatedOpenAIUsage sets every field this conversion could plausibly
// read, with distinct values so a mis-wired field is visible rather than
// coincidentally equal.
func fullyPopulatedOpenAIUsage() dto.Usage {
	return dto.Usage{
		PromptTokens:     3527,
		CompletionTokens: 41,
		TotalTokens:      3568,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         3456,
			CachedCreationTokens: 17,
		},
		PromptTokensIncludeCached: true,
	}
}

// terminalStreamUsage drives a full stream and returns the usage carried on the
// message_delta event — the streaming counterpart of ResponseOpenAI2Claude's
// response body.
func terminalStreamUsage(t *testing.T, u dto.Usage) *dto.ClaudeUsage {
	t.Helper()
	stop := "stop"
	info := claudeStreamInfo(int(u.PromptTokens))
	done := chunk(streamChoice("", &stop))
	usage := u
	done.Usage = &usage

	for _, ev := range feedClaudeStream(info, chunk(streamChoice("hi", nil)), done) {
		if ev.Type == "message_delta" && ev.Usage != nil {
			return ev.Usage
		}
	}
	t.Fatal("stream produced no message_delta carrying usage")
	return nil
}

func TestClaudeTerminalUsage_StreamAndNonStreamAgree(t *testing.T) {
	u := fullyPopulatedOpenAIUsage()

	nonStream := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id:      "resp",
		Model:   "m",
		Choices: []dto.OpenAITextResponseChoice{textChoice("hi", "stop")},
		Usage:   u,
	}, nonOpenRouterInfo()).Usage

	stream := terminalStreamUsage(t, u)

	if !reflect.DeepEqual(nonStream, stream) {
		t.Errorf("terminal usage differs by transport.\n non-stream: %+v\n     stream: %+v\n"+
			"A Claude-wire client must get the same numbers for the same request whether or not it "+
			"streamed; this divergence is exactly how non-streamed replies shipped with "+
			"cache_read_input_tokens=0 while being billed at the cache discount.",
			nonStream, stream)
	}
}

// TestClaudeTerminalUsage_CarriesEveryFieldItCan is the census half: every
// numeric field of ClaudeUsage that has a source in dto.Usage must actually be
// carried. Fields with no reachable source are listed explicitly with the
// reason, so adding a new one forces a decision instead of silently shipping a
// zero.
func TestClaudeTerminalUsage_CarriesEveryFieldItCan(t *testing.T) {
	u := fullyPopulatedOpenAIUsage()
	got := claudeTerminalUsage(&u)

	carried := map[string]int{
		// Anthropic-wire semantics: input_tokens excludes the cache read and
		// cache creation terms. The fixture is flagged as an includes-cached wire
		// (OpenAI/Gemini/xAI), so the Claude-wire figure is prompt minus both:
		// 3527 - 3456 - 17 = 54. Until 2026-09-02 this line expected u.PromptTokens
		// (3527), which pinned the SOURCE semantics onto the Claude wire and made a
		// Claude SDK count the 3456 cached tokens twice.
		"InputTokens":              u.PromptTokens - u.PromptTokensDetails.CachedTokens - u.PromptTokensDetails.CachedCreationTokens,
		"OutputTokens":             u.CompletionTokens,
		"CacheCreationInputTokens": u.PromptTokensDetails.CachedCreationTokens,
		"CacheReadInputTokens":     u.PromptTokensDetails.CachedTokens,
	}
	// Documented as unreachable from this conversion — see claudeTerminalUsage's
	// comment. If one of these becomes reachable, move it into `carried`.
	unsourced := map[string]string{
		"CacheCreation":               "pointer to the Anthropic 5m/1h breakdown object; no OpenAI-wire upstream emits it",
		"ClaudeCacheCreation5mTokens": "no upstream reachable from this conversion populates the split",
		"ClaudeCacheCreation1hTokens": "no upstream reachable from this conversion populates the split",
		"ServerToolUse":               "Anthropic server-tool block; not produced by an OpenAI-wire upstream",
	}

	v := reflect.ValueOf(*got)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if want, ok := carried[name]; ok {
			if int(v.Field(i).Int()) != want {
				t.Errorf("ClaudeUsage.%s = %d, want %d", name, v.Field(i).Int(), want)
			}
			continue
		}
		if _, ok := unsourced[name]; ok {
			continue
		}
		t.Errorf("ClaudeUsage.%s is neither carried by claudeTerminalUsage nor listed as unsourced. "+
			"A new usage field that nobody maps ships as a silent zero to every Claude-wire client — "+
			"either map it or record here why it has no source.", name)
	}
}

// TestClaudeTerminalUsage_NilIn is the boundary: a stream chunk can arrive with
// no usage at all, and the caller relies on a nil result to skip the event.
func TestClaudeTerminalUsage_NilIn(t *testing.T) {
	if got := claudeTerminalUsage(nil); got != nil {
		t.Errorf("claudeTerminalUsage(nil) = %+v, want nil", got)
	}
}
