package relay

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// M5 — provider census.
//
// The billing invariance matrix records "32 settlement cells, all green on the
// first run". That is a fair description and also the reason to be careful with
// it: the half of that work which actually caught defects was the VISIBILITY
// half, and the matrix covers seven upstreams out of forty-odd provider
// packages. A matrix that is green over a small subset invites the reading that
// billing is covered.
//
// This is not an instruction to expand the matrix. The plan is explicit that
// expanding it would grow a surface with no traffic behind it. What is missing
// is the *list*: which providers have had their billing semantics checked and
// which have not. This test produces that list mechanically and refuses to let
// it go stale — a provider directory that is neither in the matrix nor in the
// reasoned exclusion table below fails the build.
//
// The first run of this test named roughly two dozen providers. That list is
// the scope document.
//
// Deliberately NOT asserted here: that every provider is correct. This says
// only which ones have been looked at, which is the honest claim available.

// matrixCovered are the upstreams the billing invariance matrix drives
// end-to-end (channelType entries in billing_invariance_matrix_test.go). Keep
// this in sync when a case is added there — TestProviderBillingCensus fails if
// a name here has no provider directory, so it cannot silently rot.
var matrixCovered = map[string]string{
	"openai":   "OpenAI chat + responses wires",
	"claude":   "Anthropic wire (input_tokens excludes cache reads)",
	"gemini":   "Gemini native wire",
	"deepseek": "OpenAI wire, prompt_tokens includes cached",
	"moonshot": "OpenAI wire via own adaptor",
	"xai":      "own handler, OpenAI wire",
	"zhipu_4v": "Zhipu v4",
}

// notBilledPerToken are directories under provider/ that do not settle
// per-token LLM usage, so a token-accounting matrix cell would be meaningless
// for them. Each needs a reason, not just an entry.
var notBilledPerToken = map[string]string{
	"common":   "shared RelayInfo/adaptor plumbing, not a vendor",
	"constant": "channel-type constants, not a vendor",
	"task":     "async task relay (Midjourney/Suno); priced per call, see GenerateMjOtherInfo",
}

// awaitingCensus are real per-token providers whose billing semantics have NOT
// been checked against the matrix. This list IS the finding: every name here is
// a provider we relay tokens for and have never verified the cache/prompt
// accounting of.
//
// The rule for removing a name: either add a matrix case for it, or establish
// and record that it is byte-identical to one already covered. "It looks like
// OpenAI" is not sufficient — that assumption is exactly what produced the
// xAI defects (both transports wrong, in opposite directions) and the missing
// Gemini cachedContentTokenCount.
var awaitingCensus = map[string]string{
	"ai360":       "",
	"ali":         "",
	"aws":         "Bedrock; hosts Anthropic models but through its own usage shape",
	"baidu":       "",
	"baidu_v2":    "",
	"cloudflare":  "",
	"cohere":      "billed units, not tokens — check whether the mapping is lossy",
	"coze":        "",
	"dify":        "",
	"jimeng":      "",
	"jina":        "",
	"lingyiwanwu": "",
	"minimax":     "",
	"mistral":     "",
	"mokaai":      "",
	"ollama":      "self-hosted; usually zero-cost, confirm that is enforced",
	"openrouter":  "aggregator: its own usage accounting sits between us and the vendor",
	"palm":        "",
	"perplexity":  "",
	"replicate":   "",
	"siliconflow": "",
	"submodel":    "",
	"tencent":     "",
	"vertex":      "hosts Anthropic + Gemini models; usage shape differs from both natives",
	"volcengine":  "",
	"xinference":  "",
	"xunfei":      "",
	"zhipu":       "v3, distinct from the covered zhipu_4v",
}

// containsGoFile reports whether dir holds a .go file at any depth. Errors are
// returned rather than swallowed: an unreadable directory means the census is
// incomplete, which must fail loudly rather than shrink the list.
func containsGoFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			nested, nestedErr := containsGoFile(filepath.Join(dir, e.Name()))
			if nestedErr != nil {
				return false, nestedErr
			}
			if nested {
				return true, nil
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}

func providerDirs(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "adapter", "provider"))
	if err != nil {
		t.Fatalf("resolve provider dir: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read provider dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Count a directory as a provider if it holds Go source at ANY depth:
		// task/ is an umbrella whose real packages are task/suno, task/kling
		// and so on, with nothing at its own top level. A one-level check
		// silently dropped it, and a provider the census cannot see is exactly
		// what this test exists to prevent.
		has, hasErr := containsGoFile(filepath.Join(root, e.Name()))
		if hasErr != nil {
			t.Fatalf("read provider package %s: %v", e.Name(), hasErr)
		}
		if has {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestProviderBillingCensus is the scope document, enforced. Every provider
// package must be accounted for in exactly one of the three lists above.
func TestProviderBillingCensus(t *testing.T) {
	dirs := providerDirs(t)
	if len(dirs) < 20 {
		t.Fatalf("found only %d provider packages — the scan is not looking where it thinks", len(dirs))
	}

	present := map[string]bool{}
	for _, d := range dirs {
		present[d] = true
	}

	var unaccounted []string
	for _, d := range dirs {
		_, a := matrixCovered[d]
		_, b := notBilledPerToken[d]
		_, c := awaitingCensus[d]
		n := 0
		for _, in := range []bool{a, b, c} {
			if in {
				n++
			}
		}
		switch n {
		case 0:
			unaccounted = append(unaccounted, d)
		case 1:
			// exactly one — correct
		default:
			t.Errorf("provider %q appears in more than one census list — pick one", d)
		}
	}
	sort.Strings(unaccounted)
	if len(unaccounted) > 0 {
		t.Errorf("provider packages with no billing-census status: %v\n\n"+
			"Every provider we relay tokens for is either covered by the billing "+
			"invariance matrix, not settled per token, or explicitly awaiting review. "+
			"A provider in none of those is one whose cache and prompt accounting nobody "+
			"has looked at, and nothing says so.", unaccounted)
	}

	// The lists must not name directories that no longer exist, or the census
	// silently shrinks while looking complete.
	for _, list := range []struct {
		name    string
		entries map[string]string
	}{
		{"matrixCovered", matrixCovered},
		{"notBilledPerToken", notBilledPerToken},
		{"awaitingCensus", awaitingCensus},
	} {
		for name := range list.entries {
			if !present[name] {
				t.Errorf("%s names %q, which is not a provider package — stale entry", list.name, name)
			}
		}
	}
}

// TestProviderCensusMatchesTheMatrix keeps matrixCovered honest: a name may
// only be listed there if the matrix file actually drives that channel type.
// Without this, a provider could be marked "covered" by editing this file
// alone.
func TestProviderCensusMatchesTheMatrix(t *testing.T) {
	body, err := os.ReadFile("billing_invariance_matrix_test.go")
	if err != nil {
		t.Fatalf("read the matrix: %v", err)
	}
	text := string(body)

	// channelType constants the matrix drives, mapped to the provider package
	// that serves them. Split out so a missing mapping is a compile-time
	// obligation rather than a silent skip.
	channelTypeForProvider := map[string]string{
		"openai":   "ChannelTypeOpenAI",
		"claude":   "ChannelTypeAnthropic",
		"gemini":   "ChannelTypeGemini",
		"deepseek": "ChannelTypeDeepSeek",
		"moonshot": "ChannelTypeMoonshot",
		"xai":      "ChannelTypeXai",
		"zhipu_4v": "ChannelTypeZhipu_v4",
	}

	for provider := range matrixCovered {
		constName, ok := channelTypeForProvider[provider]
		if !ok {
			t.Errorf("matrixCovered lists %q but no channel-type constant is mapped for it — "+
				"add the mapping so this check can verify the claim", provider)
			continue
		}
		if !strings.Contains(text, "constant."+constName) {
			t.Errorf("matrixCovered claims %q is covered, but the matrix never drives "+
				"constant.%s. Either add the case or move the provider to awaitingCensus.",
				provider, constName)
		}
	}
}
