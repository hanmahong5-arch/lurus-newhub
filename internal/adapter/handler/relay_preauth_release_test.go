package handler

import (
	"errors"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// spyReturnPreConsumedQuota swaps the release seam for the duration of a test and
// returns a pointer to the per-relayInfo call counter.
func spyReturnPreConsumedQuota(t *testing.T) *[]*relaycommon.RelayInfo {
	t.Helper()
	original := returnPreConsumedQuota
	var calls []*relaycommon.RelayInfo
	returnPreConsumedQuota = func(c *gin.Context, info *relaycommon.RelayInfo) {
		calls = append(calls, info)
	}
	t.Cleanup(func() { returnPreConsumedQuota = original })
	return &calls
}

// TestReleasePreConsumedOnFailure_TrustPathStillReleases is the regression lock:
// the trust optimisation zeroes FinalPreConsumedQuota while the platform wallet
// hold stays live, so a failed request on that path must still hand the
// pre-authorization back instead of leaving it frozen until its TTL expires.
func TestReleasePreConsumedOnFailure_TrustPathStillReleases(t *testing.T) {
	calls := spyReturnPreConsumedQuota(t)
	c, _ := newTestCtx()

	info := &relaycommon.RelayInfo{
		UserId:                7,
		FinalPreConsumedQuota: 0, // trust path: nothing pre-deducted locally
		PlatformPreAuthID:     4242,
		IdentityAccountID:     99,
	}

	releasePreConsumedOnFailure(c, types.NewError(errors.New("upstream down"), types.ErrorCodeBadResponseStatusCode), info)

	if len(*calls) != 1 {
		t.Fatalf("expected 1 release for a failed trust-path request, got %d", len(*calls))
	}
	if (*calls)[0] != info {
		t.Fatalf("release called with the wrong relayInfo: %+v", (*calls)[0])
	}
}

// TestReleasePreConsumedOnFailure_OrdinaryErrorReleasesOnce guards the other
// direction: the ordinary error path (local quota really was pre-deducted) still
// hands back exactly once — the local refund and the platform release are both
// guarded inside ReturnPreConsumedQuota, so widening the gate must not double-refund.
func TestReleasePreConsumedOnFailure_OrdinaryErrorReleasesOnce(t *testing.T) {
	calls := spyReturnPreConsumedQuota(t)
	c, _ := newTestCtx()

	info := &relaycommon.RelayInfo{
		UserId:                7,
		FinalPreConsumedQuota: 5000,
		PlatformPreAuthID:     4242,
		IdentityAccountID:     99,
	}

	releasePreConsumedOnFailure(c, types.NewError(errors.New("upstream down"), types.ErrorCodeBadResponseStatusCode), info)

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 release (no double refund), got %d", len(*calls))
	}
}

// TestReleasePreConsumedOnFailure_SuccessKeepsQuota proves the gate is still a
// gate: a successful relay settles rather than releases, so nothing is handed back.
func TestReleasePreConsumedOnFailure_SuccessKeepsQuota(t *testing.T) {
	calls := spyReturnPreConsumedQuota(t)
	c, _ := newTestCtx()

	for _, quota := range []int{0, 5000} {
		info := &relaycommon.RelayInfo{UserId: 7, FinalPreConsumedQuota: quota, PlatformPreAuthID: 4242}
		releasePreConsumedOnFailure(c, nil, info)
	}

	if len(*calls) != 0 {
		t.Fatalf("successful relay must not return pre-consumed quota, got %d releases", len(*calls))
	}
}
