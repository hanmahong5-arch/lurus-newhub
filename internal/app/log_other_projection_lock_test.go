package app

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// repo.SanitizeOtherForUser is a BLACKLIST: it deletes the keys named in
// internalOtherKeys and ships everything else. Its doc comment says it "returns
// a log row's Other JSON with every TierInternal key stripped (governance
// classification)", which describes a whitelist. While those two disagree,
// every new key added to an Other map is published to ordinary customers by
// default, and nothing fails — that is how cache_creation_ratio_5m,
// cache_creation_ratio_1h and audio_input_seperate_price came to be readable by
// the people we charge with them.
//
// This test is the reconciliation, and it lives in the CI gate rather than on
// the request path on purpose. Flipping the runtime blacklist into a whitelist
// today would empty the billing-explainability panel: FieldClassification has
// no entry for cache_tokens, request_path, reasoning_effort and a dozen other
// keys the console legitimately renders, so a whitelist would drop them all.
// Default-deny belongs where a human can answer the question before shipping.
//
// The contract: run the real generators with fully non-zero inputs so every
// conditional branch fires, then assert each key they produce is named in
// exactly one of the two lists below. Adding a key to an Other map without
// deciding who may see it is a build failure.

// wantUserVisible: keys an ordinary caller may read. The rule of thumb is
// "their data, not our economics" — what they sent, what they consumed, how
// long it took.
var wantUserVisible = map[string]string{
	"cache_tokens":                 "their cache hit count; needed to reconcile a discounted charge",
	"cache_creation_tokens":        "their cache write count",
	"cache_creation_tokens_5m":     "their 5m-bucket cache write count",
	"cache_creation_tokens_1h":     "their 1h-bucket cache write count",
	"frt":                          "time to first token — the caller's own request timing, TierPublic",
	"reasoning_effort":             "an option they sent on the request",
	"request_path":                 "the endpoint they called",
	"is_system_prompt_overwritten": "disclosure that we altered their prompt; hiding it would be the leak",
	"claude":                       "which wire format their request used",
	"audio":                        "marks an audio request",
	"ws":                           "marks a realtime/websocket request",
	"audio_input":                  "their audio input token count",
	"audio_output":                 "their audio output token count",
	"text_input":                   "their text input token count",
	"text_output":                  "their text output token count",
	"audio_input_token_count":      "their audio input token count",
	"image":                        "marks an image request",
	"image_output":                 "their image output token count",
	"web_search":                   "marks that their request used web search",
	"file_search":                  "marks that their request used file search",
	"image_generation_call":        "marks that their request generated an image",

	// Error-log rows (adapter/handler/relay.go, adapter/middleware/utils.go).
	// A caller must be able to see why their own request failed.
	"error_type":   "why their request failed",
	"error_code":   "why their request failed",
	"status_code":  "the HTTP status their request got",
	"relay_mode":   "which relay mode served them; TierPublic in governance/classification.go",
	"channel_type": "which upstream vendor family served them; TierPublic in governance/classification.go, and already implied by the model they chose",

	// Deliberately NOT stripped, and the reason is worth stating because the
	// name invites the opposite conclusion: at the only site that writes it
	// (adapter/handler/relay.go, `c.GetString("original_model")`) the value is
	// the model the CALLER asked for — byte-for-byte the same string as the
	// row's public model_name column, which the same response already carries.
	// Stripping it would plug zero bits of information while leaving behind a
	// "this field is handled" credential. The real defect is that the field
	// name claims to be the upstream model and is not; fixing that means
	// renaming or dropping it at the write site, not hiding it here.
	"upstream_model": "misnamed: holds the caller's own original_model, identical to the public model_name",
}

// wantInternal: keys that must never reach a non-admin. Predominantly our
// pricing multipliers, our upstream identities and the admin routing trace.
var wantInternal = map[string]string{
	"model_ratio":             "our price multiplier",
	"group_ratio":             "our price multiplier",
	"completion_ratio":        "our price multiplier",
	"cache_ratio":             "our price multiplier",
	"cache_creation_ratio":    "our price multiplier",
	"cache_creation_ratio_5m": "our price multiplier",
	"cache_creation_ratio_1h": "our price multiplier",
	"model_price":             "our per-call price",
	"user_group_ratio":        "our per-group discount",
	"audio_ratio":             "our price multiplier",
	"audio_completion_ratio":  "our price multiplier",
	"is_model_mapped":         "reveals that we rerouted the model",
	"upstream_model_name":     "the upstream vendor model behind their request",
	"admin_info":              "channel ids, multi-key indices, the routing trace",

	"image_ratio":                 "our price multiplier",
	"audio_input_price":           "our per-token audio price",
	"audio_input_seperate_price":  "the flag half of audio_input_price",
	"web_search_price":            "our per-call search price",
	"file_search_price":           "our per-call search price",
	"image_generation_call_price": "our per-call image price",
	// Call counts are arguably the caller's own consumption, but the strip list
	// has treated them as internal since it was written and widening a
	// user-facing projection is not this change's job. Recorded here so the
	// tension is visible rather than accidental.
	"web_search_call_count":  "existing policy: stripped alongside its price",
	"file_search_call_count": "existing policy: stripped alongside its price",

	// Error-log rows name the upstream account that failed. The v2 user log
	// route blanks the channel_name COLUMN (v2_log.go), so leaving these in the
	// Other payload would have made that blanking a false credential — the same
	// value, one field over.
	"channel_id":   "which upstream account served them",
	"channel_name": "our upstream account name; TierInternal",
}

// driveGenerators runs every Other-producing generator with non-zero inputs and
// returns the union of the keys they emit. Non-zero matters: each generator
// gates several keys behind `if x != 0` / `if s != ""`, and a zero-valued call
// would silently exercise none of them — the exact blind spot that let three
// keys ship unclassified.
func driveGenerators(t *testing.T) map[string]struct{} {
	t.Helper()

	newCtx := func() *gin.Context {
		c := createTestGinContext()
		// Fire every context-gated branch in GenerateTextOtherInfo.
		c.Set(string(constant.ContextKeySystemPromptOverride), true)
		c.Set(string(constant.ContextKeyChannelIsMultiKey), true)
		c.Set(string(constant.ContextKeyChannelMultiKeyIndex), 2)
		c.Set(string(constant.ContextKeyLocalCountTokens), true)
		c.Set("use_channel", []string{"25", "26"})
		// >1 attempt so the routing trace is attached.
		RecordRouteAttempt(c, RouteAttempt{ChannelID: 25, Outcome: RouteAttemptOutcomeBreakerOpen})
		RecordRouteAttempt(c, RouteAttempt{ChannelID: 26, Outcome: RouteAttemptOutcomeSuccess, DurationMs: 120})
		return c
	}

	start := time.Now()
	newInfo := func() *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			StartTime: start,
			// After StartTime, so HasSendResponse() is true and frt is emitted.
			FirstResponseTime: start.Add(250 * time.Millisecond),
			ReasoningEffort:   "high",
			RequestURLPath:    "/v1/chat/completions?stream=true",
			ChannelMeta: &relaycommon.ChannelMeta{
				// Fires the is_model_mapped / upstream_model_name branch.
				IsModelMapped:     true,
				UpstreamModelName: "gpt-4o-2024-11-20",
			},
		}
	}

	keys := map[string]struct{}{}
	collect := func(m map[string]interface{}) {
		for k := range m {
			keys[k] = struct{}{}
		}
	}

	collect(GenerateTextOtherInfo(newCtx(), newInfo(),
		3.0, 1.1, 5.0, // modelRatio, groupRatio, completionRatio
		100, 0.5, // cacheTokens, cacheRatio
		0.01, 0.9)) // modelPrice, userGroupRatio

	collect(GenerateClaudeOtherInfo(newCtx(), newInfo(),
		3.0, 1.1, 5.0,
		100, 0.5,
		200, 2.0, // cacheCreationTokens, cacheCreationRatio
		50, 1.25, // 5m tokens/ratio — non-zero so the bucket branch fires
		30, 2.0, // 1h tokens/ratio
		0.01, 0.9))

	audioUsage := &dto.Usage{}
	audioUsage.PromptTokensDetails.AudioTokens = 12
	audioUsage.PromptTokensDetails.TextTokens = 34
	audioUsage.CompletionTokenDetails.AudioTokens = 56
	audioUsage.CompletionTokenDetails.TextTokens = 78
	collect(GenerateAudioOtherInfo(newCtx(), newInfo(), audioUsage,
		3.0, 1.1, 5.0, 1.5, 2.5, 0.01, 0.9))

	wssUsage := &dto.RealtimeUsage{}
	wssUsage.InputTokenDetails.AudioTokens = 12
	wssUsage.InputTokenDetails.TextTokens = 34
	wssUsage.OutputTokenDetails.AudioTokens = 56
	wssUsage.OutputTokenDetails.TextTokens = 78
	collect(GenerateWssOtherInfo(newCtx(), newInfo(), wssUsage,
		3.0, 1.1, 5.0, 1.5, 2.5, 0.01, 0.9))

	collect(GenerateMjOtherInfo(newInfo(), types.PerCallPriceData{
		ModelPrice: 0.02,
		Quota:      1000,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio:        1.1,
			GroupSpecialRatio: 0.8,
			HasSpecialRatio:   true,
		},
	}))

	return keys
}

// scanOtherLiteralKeys walks every non-test Go file under internal/ and returns
// the string-literal keys assigned into a map named `other`.
//
// Running the four generators is not enough coverage on its own: the settlement
// path adds more keys to the same map AFTER the generator returns
// (relay/compatible_handler.go), and the error-log path builds its own `other`
// from scratch (adapter/handler/relay.go, adapter/middleware/utils.go). Both
// end up in the same log column behind the same projection, and the first of
// those is where audio_input_seperate_price was hiding — a generator-only lock
// would have had a hole exactly where one of the three known leaks lived.
//
// Every file in internal/ that writes `other["..."]` today is a log-Other
// producer, so this needs no exclusion list; if an unrelated map named `other`
// ever appears, the failure is a loud "classify this key", not a silent miss.
func scanOtherLiteralKeys(t *testing.T) map[string]string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	internalDir := filepath.Join(root, "internal")

	found := map[string]string{} // key -> "file:line" of the first writer
	err = filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				idx, ok := lhs.(*ast.IndexExpr)
				if !ok {
					continue
				}
				ident, ok := idx.X.(*ast.Ident)
				if !ok || ident.Name != "other" {
					continue
				}
				lit, ok := idx.Index.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				key, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					continue
				}
				if _, seen := found[key]; !seen {
					rel, _ := filepath.Rel(root, path)
					found[key] = fmt.Sprintf("%s:%d", filepath.ToSlash(rel), fset.Position(lit.Pos()).Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("scanner found no other[\"...\"] assignments at all — it is measuring nothing")
	}
	return found
}

// allOtherKeys unions the runtime-observed generator keys with the statically
// scanned ones. Both halves are needed: the scan cannot see keys built from
// non-literal expressions, and the runtime drive cannot see the settlement and
// error-log writers.
func allOtherKeys(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	for k, where := range scanOtherLiteralKeys(t) {
		keys[k] = where
	}
	for k := range driveGenerators(t) {
		if _, seen := keys[k]; !seen {
			keys[k] = "internal/app/log_info_generate.go (observed at runtime)"
		}
	}
	return keys
}

// TestOtherProjectionIsFullyClassified is the default-deny gate. Every key that
// can reach the Other column must be declared user-visible or internal; an
// unlisted key fails the build rather than shipping to customers.
func TestOtherProjectionIsFullyClassified(t *testing.T) {
	keys := allOtherKeys(t)

	var unclassified []string
	for k := range keys {
		_, pub := wantUserVisible[k]
		_, internal := wantInternal[k]
		switch {
		case pub && internal:
			t.Errorf("%q is declared BOTH user-visible and internal — pick one", k)
		case !pub && !internal:
			unclassified = append(unclassified, fmt.Sprintf("%s (%s)", k, keys[k]))
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("Other keys with no visibility decision: %v\n\n"+
			"SanitizeOtherForUser is a blacklist, so an unlisted key is published to every "+
			"ordinary customer. Decide who may see it, then either add it to wantUserVisible "+
			"or add it to BOTH wantInternal and repo.internalOtherKeys.", unclassified)
	}
}

// TestOtherProjectionStripsInternalKeys proves the decision above is actually
// enforced at runtime: build a payload out of every key that can reach the
// Other column, run it through the real user projection, and check what
// survives. TestOtherProjectionIsFullyClassified alone would pass with an empty
// internalOtherKeys — the declaration has to be wired to the strip list.
func TestOtherProjectionStripsInternalKeys(t *testing.T) {
	keys := allOtherKeys(t)

	payload := map[string]interface{}{}
	for k := range keys {
		// Value shape is irrelevant to the strip list; only key presence is.
		payload[k] = 1
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal probe payload: %v", err)
	}

	var got map[string]interface{}
	sanitized := repo.SanitizeOtherForUser(string(raw))
	if err := json.Unmarshal([]byte(sanitized), &got); err != nil {
		t.Fatalf("sanitized payload is not valid JSON (%q): %v", sanitized, err)
	}

	for k, why := range wantInternal {
		if _, present := keys[k]; !present {
			// The generators no longer emit it; nothing to enforce.
			continue
		}
		if _, leaked := got[k]; leaked {
			t.Errorf("user projection leaked %q (%s) — declared internal but absent from "+
				"repo.internalOtherKeys", k, why)
		}
	}
	for k, why := range wantUserVisible {
		if _, present := keys[k]; !present {
			continue
		}
		if _, kept := got[k]; !kept {
			t.Errorf("user projection dropped %q (%s) — declared user-visible but stripped "+
				"by repo.internalOtherKeys", k, why)
		}
	}
}
