package handler

// client_profile_display_amount_single_source_test.go — the client profile's
// display_amount must come from the same conversion as every other page.
//
// handler had its own calculateDisplayAmount until 2026-09-28, a copy of
// app.CalculateDisplayAmount that predated the LUTE display type: under
// QuotaDisplayType=LUTE the profile and subscription endpoints returned
// quota/QuotaPerUnit (a USD figure) while the rest of the console showed
// currency.LutToLucDisplay(quota). Mutation oracle: restore the private copy
// and this reads the USD figure.

import (
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

func TestClientGetProfile_LuteDisplayType_UsesTheSharedConversion(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	gs := operation_setting.GetGeneralSetting()
	original := gs.QuotaDisplayType
	gs.QuotaDisplayType = operation_setting.QuotaDisplayTypeLute
	defer func() { gs.QuotaDisplayType = original }()

	user, err := repo.GetUserById(ctx.NormalUser.Id, false)
	if err != nil {
		t.Fatal(err)
	}
	if user.Quota == 0 {
		t.Fatal("fixture user has zero quota; the two conversions would agree trivially")
	}

	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/client/profile", nil, map[string]string{
		"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id),
	})
	AssertV2Status(t, w, http.StatusOK)
	data := AssertV2Success(t, w)["data"].(map[string]interface{})
	got, _ := data["display_amount"].(float64)

	want := currency.LutToLucDisplay(user.Quota)
	usd := float64(user.Quota) / common.QuotaPerUnit
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("display_amount under LUTE = %v, want the shared conversion %v (the retired handler copy gave the USD figure %v)", got, want, usd)
	}
	if math.Abs(want-usd) < 1e-9 {
		t.Fatalf("test cannot discriminate: Lute display %v equals the USD figure %v for quota %d", want, usd, user.Quota)
	}
}
