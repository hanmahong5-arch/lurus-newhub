package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// fetchTokenRateLimits reads the persisted rpm/tpm columns straight from the
// test DB so assertions hit storage, not the handler's response echo.
func fetchTokenRateLimits(t *testing.T, ctx *V2TestContext, id int) (rpm, tpm int) {
	t.Helper()
	var tok repo.Token
	if err := ctx.DB.First(&tok, "id = ?", id).Error; err != nil {
		t.Fatalf("fetch token %d: %v", id, err)
	}
	return tok.RateLimitRPM, tok.RateLimitTPM
}

func TestCreateTokenV2_RateLimitsPersisted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Rate Limited Token",
		"unlimited_quota": true,
		"rate_limit_rpm":  120,
		"rate_limit_tpm":  90000,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	resp := AssertV2Success(t, w)

	id := int(resp["data"].(map[string]interface{})["id"].(float64))
	rpm, tpm := fetchTokenRateLimits(t, ctx, id)
	if rpm != 120 || tpm != 90000 {
		t.Errorf("expected persisted rpm=120 tpm=90000, got rpm=%d tpm=%d", rpm, tpm)
	}
}

func TestCreateTokenV2_RateLimitNegativeRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Bad Limits Token",
		"unlimited_quota": true,
		"rate_limit_rpm":  -1,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestUpdateTokenV2_RateLimitsSetAndReset(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Limits Update Token")
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)

	// Set both limits.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{
		"rate_limit_rpm": 5,
		"rate_limit_tpm": 1000,
	}, nil)
	AssertV2Status(t, w, http.StatusOK)
	rpm, tpm := fetchTokenRateLimits(t, ctx, token.Id)
	if rpm != 5 || tpm != 1000 {
		t.Fatalf("expected rpm=5 tpm=1000 after update, got rpm=%d tpm=%d", rpm, tpm)
	}

	// Omitted fields stay unchanged.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{
		"name": "Limits Update Token Renamed",
	}, nil)
	AssertV2Status(t, w, http.StatusOK)
	rpm, tpm = fetchTokenRateLimits(t, ctx, token.Id)
	if rpm != 5 || tpm != 1000 {
		t.Fatalf("expected limits unchanged when omitted, got rpm=%d tpm=%d", rpm, tpm)
	}

	// Explicit 0 resets to unlimited (pointer binding distinguishes 0 from omitted).
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{
		"rate_limit_rpm": 0,
		"rate_limit_tpm": 0,
	}, nil)
	AssertV2Status(t, w, http.StatusOK)
	rpm, tpm = fetchTokenRateLimits(t, ctx, token.Id)
	if rpm != 0 || tpm != 0 {
		t.Fatalf("expected limits reset to 0, got rpm=%d tpm=%d", rpm, tpm)
	}

	// Invalid update leaves the row untouched.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, map[string]interface{}{
		"rate_limit_rpm": -3,
	}, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	rpm, tpm = fetchTokenRateLimits(t, ctx, token.Id)
	if rpm != 0 || tpm != 0 {
		t.Fatalf("expected limits untouched after invalid update, got rpm=%d tpm=%d", rpm, tpm)
	}
}
