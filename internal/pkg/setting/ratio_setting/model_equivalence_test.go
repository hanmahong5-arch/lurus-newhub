package ratio_setting

import "testing"

func TestResolveSourceProduct(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"known lowercase", "kova", "kova"},
		{"known mixed case", "Lutu", "lutu"},
		{"known with whitespace", "  lucrum  ", "lucrum"},
		{"unknown falls back", "evil-injected", DefaultSourceProduct},
		{"empty falls back", "", DefaultSourceProduct},
		{"default itself", DefaultSourceProduct, DefaultSourceProduct},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSourceProduct(tc.header); got != tc.want {
				t.Errorf("ResolveSourceProduct(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}

	// The fallback is written into every untagged relay's wallet debit, so it
	// must name a product the platform catalog actually has — "llm-api", seeded
	// by platform migrations/003_seed_products.sql. Asserted as a literal (not
	// via DefaultSourceProduct) because the constant is exactly what can drift:
	// a value with no catalog row splits relay revenue off the product
	// dimension and gets every debit rejected once the platform's
	// billing.require_product_attribution ratchet is on.
	t.Run("default names a real catalog product", func(t *testing.T) {
		if got := ResolveSourceProduct(""); got != "llm-api" {
			t.Errorf("ResolveSourceProduct(\"\") = %q, want %q", got, "llm-api")
		}
	})
}

// TestModelEquivalence_SubsResolve asserts the curated map's invariants:
// every non-empty substitute resolves to a real (non-zero) GetModelRatio and is
// never the model itself. This guards against a typo silently producing a
// "free" alternative that fabricates 100% savings.
func TestModelEquivalence_SubsResolve(t *testing.T) {
	InitRatioSettings()

	for model, eq := range ModelEquivalenceMap() {
		for label, sub := range map[string]string{
			"conservative": eq.ConservativeSub,
			"aggressive":   eq.AggressiveSub,
		} {
			if sub == "" {
				continue
			}
			if sub == model {
				t.Errorf("%s: %s substitute is the model itself", model, label)
			}
			ratio, ok, _ := GetModelRatio(sub)
			if !ok || ratio <= 0 {
				t.Errorf("%s: %s substitute %q has no positive ratio (ratio=%v ok=%v)",
					model, label, sub, ratio, ok)
			}
		}
		// Reasoning models must carry no substitutes (they're source-excluded).
		if eq.Reasoning && (eq.ConservativeSub != "" || eq.AggressiveSub != "") {
			t.Errorf("%s: reasoning model must not declare substitutes", model)
		}
	}
}
