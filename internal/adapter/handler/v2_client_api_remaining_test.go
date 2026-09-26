package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestClientAPI_RemainingQuotaIsQuotaNotQuotaMinusUsed pins the balance the
// client API reports. users.quota is already the remainder (settlement debits
// it and credits used_quota), so quota - used_quota subtracted lifetime spend a
// second time: a customer who had spent 300 of 1000 was shown 400, not 700,
// and a heavy user went negative. The old tests only checked the field exists.
func TestClientAPI_RemainingQuotaIsQuotaNotQuotaMinusUsed(t *testing.T) {
	ctx := setupClientAPIRouter(t)
	defer ctx.Cleanup()

	const remaining, used = 700, 300
	if err := repo.DB.Model(&repo.User{}).Where("id = ?", ctx.NormalUser.Id).
		Updates(map[string]any{"quota": remaining, "used_quota": used}).Error; err != nil {
		t.Fatalf("seed quota: %v", err)
	}
	headers := map[string]string{"X-Test-User-ID": fmt.Sprintf("%d", ctx.NormalUser.Id)}

	for _, path := range []string{"/api/v2/client/profile", "/api/v2/client/usage/summary"} {
		w := V2Request(ctx.Router, http.MethodGet, path, nil, headers)
		AssertV2Status(t, w, http.StatusOK)
		data := AssertV2Success(t, w)["data"].(map[string]interface{})
		if got := int(data["remaining_quota"].(float64)); got != remaining {
			t.Errorf("%s remaining_quota = %d, want %d (quota=%d, used=%d)", path, got, remaining, remaining, used)
		}
		if got, want := data["display_amount"], calculateDisplayAmount(remaining); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s display_amount = %v, want %v", path, got, want)
		}
	}
}
