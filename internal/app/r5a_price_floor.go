package app

// r5a_price_floor.go — G1 (lane L-A, 2026-08-27 live-defect round). The
// Claude-native path (quota.go) and the OpenAI-compatible path
// (relay/compatible_handler.go) each carry a post-hoc floor that is supposed
// to guarantee "a nonzero-priced call never settles to exactly 0 quota" —
// but that floor's own guard predicate keyed on modelRatio (quota.go) or
// modelRatio*groupRatio (compatible_handler.go), and under UsePrice
// (per-call pricing) helper.ModelPriceHelper
// (internal/app/relay/helper/price.go:48-140) never assigns modelRatio at all
// — it stays at its Go zero value, 0.0, because the assignment only happens
// inside that function's `if !usePrice` branch (which opens at :64; the
// assignment itself is at :71). So for every
// per-call-priced model the floor's guard was always false and the floor
// never fired: a modelPrice*groupRatio*QuotaPerUnit(500000) that rounds to 0
// (i.e. modelPrice*groupRatio < 1e-6) settled the call to exactly 0 quota,
// i.e. free, even though the upstream had already served a real response.
// The sibling audio path (quota.go's calculateAudioQuota, the
// `if info.ModelPrice != 0 && rounded == 0` guard at quota.go:91) was fixed
// for this same class of bug already — this brings the other two paths in
// line with it instead of inventing a third predicate.
//
// ChargeableInputNonZero is the single predicate now shared by all four call
// sites (quota.go:332/400 wrap the Claude-native floors; compatible_handler.go
// wraps the same floor for its two branches — see the call sites themselves
// for exact line numbers, which move as the surrounding code changes). Before
// this change those four sites used three different spellings across two
// files (`modelRatio != 0` in quota.go, `!ratio.IsZero()` — i.e.
// `modelRatio*groupRatio != 0` — in compatible_handler.go, and
// `ModelPrice != 0` in the already-shipped audio floor); this collapses the
// two non-audio spellings into one function so the two settlement paths
// agree on what "the input price was nonzero" means.
//
// Deliberate, documented behavior delta: this predicate keys on
// modelPrice/modelRatio alone, not on groupRatio or OtherRatios. That matches
// the shipped audio floor only on its UsePrice half — quota.go:91's
// `info.ModelPrice != 0 && rounded == 0`. calculateAudioQuota's !UsePrice half
// still spells its guard `!ratio.IsZero()` (quota.go:119 and :128, where
// ratio = groupRatio*modelRatio, built at :103), so with
// !UsePrice && groupRatio==0 an audio call still settles to 0 while a text
// call now settles to 1. Bringing audio's non-per-call half into line is NOT
// part of this change; it is the same one-line edit and is listed in this
// round's open gaps. At least the following consequences relative to today's
// `!ratio.IsZero()` spelling in compatible_handler.go (this is what I have
// actually probed, not a claim that it's exhaustive):
//   - Under UsePrice with groupRatio==0 ("free group"), a per-call-priced
//     call now settles to 1 quota unit (=$0.000002) instead of 0 — this is
//     the actual G1 fix.
//   - Under !UsePrice with groupRatio==0, compatible_handler.go's floor at
//     what is currently line 374 (guarded by `!ratio.IsZero()`, i.e.
//     modelRatio*groupRatio != 0) also starts firing purely off modelRatio,
//     matching quota.go's Claude-native floor at line 332 (which already
//     ignored groupRatio, since it always used `modelRatio != 0`). That
//     specific site was NOT reachable under UsePrice (it lives inside
//     compatible_handler.go's `if !relayInfo.PriceData.UsePrice` block), so
//     this half of the change is a cross-path consistency fix, not part of
//     the UsePrice bug itself.
//   - Under UsePrice with a zero-valued entry in
//     relayInfo.PriceData.OtherRatios, compatible_handler.go's OtherRatios
//     multiplication loop (currently lines 391-397) can multiply
//     quotaCalculateDecimal down to 0 even though modelPrice != 0.
//     Positionally, that loop sits between the two floors, not before both:
//     compatible_handler.go's `if !relayInfo.PriceData.UsePrice` block spans
//     :313-377 and contains the first floor at :374, so on the !UsePrice path
//     the loop runs AFTER that floor; it runs before only the second floor,
//     at :422. That is why :422 is the one that ends up mattering here.
//     types.PriceData.AddOtherRatio rejects ratio<=0
//     (internal/pkg/types/price_data.go:33), but grep for direct map writes
//     to OtherRatios outside _test.go finds at least these four production
//     sites bypassing that guard:
//     internal/app/relay/relay_task.go:117,
//     internal/adapter/provider/task/vertex/adaptor.go:179,
//     internal/adapter/provider/task/ali/adaptor.go:347, and
//     internal/adapter/provider/common/relay_utils.go:176-181 (the sora-2
//     task path). The relay_utils one cannot reach 0 in practice — it clamps
//     `seconds <= 0` up to 4 at :166-168 before building the map — but it is
//     the same unguarded shape and would inherit the bug if that clamp moved.
//     The ali one can genuinely land on 0: OtherRatios["seconds"] there comes
//     from aliReq.Parameters.Duration (same file, :339), which is set
//     unchecked from a client-supplied "seconds" string at :311-317 — a
//     request with seconds="0" produces Duration=0 and thus a 0 factor.
//     Before this fix, `!ratio.IsZero()` was always false under UsePrice, so
//     the post-hoc floor at what is currently line 422 never ran and such a
//     call settled to quota==0. I measured this myself (not just citing the
//     ledger): sed'd both compatible_handler.go call sites of
//     ChargeableInputNonZero back to `!ratio.IsZero()`, drove
//     postConsumeQuota with UsePrice=true, ModelPrice=0.02,
//     OtherRatios={"seconds":0} — billed 0; reverted the sed — billed 1.
//     Caveat on that measurement: it came from a throwaway probe, not a
//     resident test. No test in the tree currently pins the
//     UsePrice + zero-OtherRatios case; the resident lock
//     (relay/r5a_compatible_percall_floor_test.go's
//     TestR5ACompatiblePerCallFloor_WiredThroughRealProducer) covers the
//     plain per-call sub-unit case instead.
//
// If the operator would rather keep free-group per-call calls free, the
// predicate needs an added `&& groupRatio != 0` term, and the four floors
// listed above (quota.go:332, quota.go:400, compatible_handler.go:374,
// compatible_handler.go:422) plus quota.go:91's already-shipped audio guard
// would all need it to stay in agreement — flagged in this round's open gaps
// rather than decided here.
func ChargeableInputNonZero(usePrice bool, modelPrice, modelRatio float64) bool {
	if usePrice {
		return modelPrice != 0
	}
	return modelRatio != 0
}
