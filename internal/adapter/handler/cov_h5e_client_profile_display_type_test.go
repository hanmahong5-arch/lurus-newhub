package handler

// cov_h5e_client_profile_display_type_test.go — covers ClientGetProfile's
// display_currency switch for the CNY and TOKENS branches (the existing
// happy-path tests only ever run under the package-default USD setting, so
// "cny"/"tokens" were never actually produced). GetGeneralSetting() returns
// a pointer to the live package-level setting — mutated and restored
// synchronously within a single test, matching the pattern already used by
// operation_setting's own tests.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

func TestClientGetProfile_CNYDisplayType_ReflectsInResponse(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	gs := operation_setting.GetGeneralSetting()
	original := gs.QuotaDisplayType
	gs.QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	defer func() { gs.QuotaDisplayType = original }()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/profile", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["display_currency"] != "cny" {
		t.Errorf("display_currency = %v, want cny", data["display_currency"])
	}
}

func TestClientGetProfile_TokensDisplayType_ReflectsInResponse(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	gs := operation_setting.GetGeneralSetting()
	original := gs.QuotaDisplayType
	gs.QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	defer func() { gs.QuotaDisplayType = original }()

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/profile", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["display_currency"] != "tokens" {
		t.Errorf("display_currency = %v, want tokens", data["display_currency"])
	}
}
