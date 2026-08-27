package app

// r1_quota_audio_floor_test.go — R1 lane, A4. calculateAudioQuota (quota.go,
// shared by PostWssConsumeQuota and PostAudioConsumeQuota) had two related
// gaps versus its Claude-native sibling (PostClaudeConsumeQuota) and the
// OpenAI-compatible sibling (relay/compatible_handler.go):
//
//   - the UsePrice (per-call price) branch used a bare `int(quota.IntPart())`
//     — pure truncation, no rounding at all — so e.g. an exact 6.5 settled to
//     6, not 7;
//   - the non-UsePrice branch's pre-round guard (:118-121 in quota.go,
//     "quota<=0") does not cover 0<quota<0.5: that range still rounds to 0
//     with no floor, silently settling a real, already-served audio call to
//     a free one.
//
// Table-driven directly against calculateAudioQuota (unexported, same
// package) since it is a pure function.

import "testing"

func TestR1CalculateAudioQuota_UsePriceRounding(t *testing.T) {
	cases := []struct {
		name       string
		modelPrice float64
		groupRatio float64
		want       int
	}{
		// modelPrice * QuotaPerUnit(500000) * groupRatio = 6.5 exactly ->
		// round-half-up must give 7, not floor(6.5)=6.
		{"exact-half-rounds-up", 0.000013, 1.0, 7},
		// Regression guard: an already-integer result must be unaffected by
		// switching truncation to rounding.
		{"exact-integer-unchanged", 0.00002, 1.0, 10},
		// Regression guard: a non-half fraction rounds to the nearer integer
		// (6.85 -> 7), distinct from the pre-fix truncating behavior (which
		// would have floored this to 6).
		{"ordinary-value-rounds-nearest", 0.00001, 1.37, 7}, // 0.00001*500000*1.37 = 6.85
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := QuotaInfo{
				UsePrice:   true,
				ModelPrice: tc.modelPrice,
				GroupRatio: tc.groupRatio,
			}
			if got := calculateAudioQuota(info); got != tc.want {
				t.Errorf("calculateAudioQuota(UsePrice, modelPrice=%v, groupRatio=%v) = %d, want %d",
					tc.modelPrice, tc.groupRatio, got, tc.want)
			}
		})
	}
}

// TestR1CalculateAudioQuota_UsePriceSubHalfUnitFloorsToOne — A4 (R1 lane).
// Adversarial round found the UsePrice branch's post-hoc floor
// (`if info.ModelPrice != 0 && rounded == 0 { rounded = 1 }`, quota.go) had
// ZERO coverage: replacing its condition with `if false && ...` left
// internal/app and its nine dependent packages fully green. The three cases
// in TestR1CalculateAudioQuota_UsePriceRounding above all land on rounded>=6
// (never 0), so none of them exercise this line. This case is the smallest
// input that rounds to exactly 0 pre-floor: modelPrice=1e-9, groupRatio=1 =>
// modelPrice*QuotaPerUnit(500000)*groupRatio = 0.0005, Round(0) = 0 without
// the floor, 1 with it. modelPrice*groupRatio < 1e-6 is the trigger
// threshold — reachable whenever an admin configures a sub-$0.000001
// per-call model price.
func TestR1CalculateAudioQuota_UsePriceSubHalfUnitFloorsToOne(t *testing.T) {
	info := QuotaInfo{
		UsePrice:   true,
		ModelPrice: 1e-9,
		GroupRatio: 1.0,
	}
	const want = 1 // 0.0005 rounds to 0 pre-floor; the floor must lift it to 1
	if got := calculateAudioQuota(info); got != want {
		t.Errorf("calculateAudioQuota(UsePrice, modelPrice=1e-9, groupRatio=1) = %d, want %d (sub-half-unit UsePrice calc must floor to 1, not settle free)", got, want)
	}
}

func TestR1CalculateAudioQuota_SubHalfUnitFloorsToOne(t *testing.T) {
	// modelRatio=0.02, groupRatio=1, completionRatio=1 (via
	// ratio_setting.GetCompletionRatio default — text tokens only, no audio):
	// calc = (textIn(10) + textOut(2)*completionRatio) * ratio(0.02) = 0.24 ->
	// Round(0) = 0 without the floor, 1 with it.
	info := QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: 10},
		OutputDetails: TokenDetails{TextTokens: 2},
		ModelName:     "r1-audio-floor-probe-model", // unregistered -> ratio_setting defaults (completionRatio=1, audioRatio=0)
		ModelRatio:    0.02,
		GroupRatio:    1.0,
	}
	const want = 1
	if got := calculateAudioQuota(info); got != want {
		t.Errorf("calculateAudioQuota sub-half-unit case = %d, want %d (0<calc<0.5 must floor to 1, not settle free)", got, want)
	}
}

func TestR1CalculateAudioQuota_NonUsePrice_RegressionGuards(t *testing.T) {
	// A value already >=1 unit must be unchanged by adding the post-round
	// floor (the floor only fires when rounded==0).
	info := QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: 100},
		OutputDetails: TokenDetails{TextTokens: 50},
		ModelName:     "r1-audio-floor-probe-model-2",
		ModelRatio:    1.0,
		GroupRatio:    1.0,
	}
	// calc = (100 + 50*1) * 1 * 1 = 150, comfortably above the floor.
	if got := calculateAudioQuota(info); got != 150 {
		t.Errorf("calculateAudioQuota regression case = %d, want 150 (unaffected by the new floor)", got)
	}

	// modelRatio==0 (or groupRatio==0) must still yield 0, not be force-floored
	// to 1 — the floor is gated on !ratio.IsZero(), same predicate as the
	// pre-round guard it mirrors.
	zeroRatioInfo := QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: 10},
		OutputDetails: TokenDetails{TextTokens: 2},
		ModelName:     "r1-audio-floor-probe-model-3",
		ModelRatio:    0,
		GroupRatio:    1.0,
	}
	if got := calculateAudioQuota(zeroRatioInfo); got != 0 {
		t.Errorf("calculateAudioQuota with modelRatio=0 = %d, want 0 (zero ratio must not be force-floored)", got)
	}
}
