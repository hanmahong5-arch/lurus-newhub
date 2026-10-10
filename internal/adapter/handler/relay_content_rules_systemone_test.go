package handler

// /v1/systemone used to be outside contentFormatFor, so a tenant's mask/reject
// rules never saw its state at all. These pin the relay-entry behaviour for it.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/tidwall/gjson"
)

const systemOneRelayBody = `{"state":"call ` + dpPhone + ` now","model":"model-a",` +
	`"questions":{"q1":{"type":"noul","instructions":"is ` + dpPhone + ` urgent?"}}}`

func TestApplyContentRules_SystemOneMasksUpstreamBody(t *testing.T) {
	f := setupDataPolicy(t)
	seedRule(t, "tenant", f.TenantID, "mask", "enforce", "phone_cn")
	c, _ := relayCtx(f.TenantID, systemOneRelayBody)
	if err := applyContentRules(c, types.RelayFormatSystemOne); err != nil {
		t.Fatal(err)
	}
	got, _ := common.GetRequestBody(c)
	if strings.Contains(string(got), dpPhone) {
		t.Fatalf("PII reached the upstream body: %s", got)
	}
	if gjson.GetBytes(got, "state").String() != "call [PHONE] now" ||
		gjson.GetBytes(got, "questions.q1.instructions").String() != "is [PHONE] urgent?" {
		t.Errorf("unexpected rewrite: %s", got)
	}
	if c.Request.ContentLength != int64(len(got)) {
		t.Errorf("content length %d != body %d", c.Request.ContentLength, len(got))
	}
}

func TestApplyContentRules_SystemOneRejectIs400(t *testing.T) {
	f := setupDataPolicy(t)
	r := seedRule(t, "tenant", f.TenantID, "reject", "enforce", "phone_cn")
	c, _ := relayCtx(f.TenantID, systemOneRelayBody)
	err := applyContentRules(c, types.RelayFormatSystemOne)
	if err == nil {
		t.Fatal("not rejected")
	}
	if err.GetErrorCode() != types.ErrorCodeContentRejected || err.StatusCode != http.StatusBadRequest {
		t.Errorf("code=%s status=%d", err.GetErrorCode(), err.StatusCode)
	}
	if msg := err.Error(); !strings.Contains(msg, strconv.FormatInt(r.Id, 10)) || strings.Contains(msg, dpPhone) {
		t.Errorf("message must carry the rule id and never the content: %q", msg)
	}
}

func TestApplyContentRules_SystemOneObserveDoesNotRewrite(t *testing.T) {
	f := setupDataPolicy(t)
	seedRule(t, "tenant", f.TenantID, "mask", "observe", "phone_cn")
	seedRule(t, "tenant", f.TenantID, "reject", "observe", "phone_cn")
	c, _ := relayCtx(f.TenantID, systemOneRelayBody)
	if err := applyContentRules(c, types.RelayFormatSystemOne); err != nil {
		t.Fatalf("observe rejected: %v", err)
	}
	if got, _ := common.GetRequestBody(c); string(got) != systemOneRelayBody {
		t.Errorf("observe altered the body: %s", got)
	}
}
