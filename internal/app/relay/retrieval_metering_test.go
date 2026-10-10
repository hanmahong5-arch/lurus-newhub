package relay

// retrieval_metering_test.go — cycle 22 L2: rerank search-unit settlement and
// the unified metering columns (usage_unit / usage_quantity / usage_source /
// retrieval_documents) written by postConsumeQuota. Drives the real price
// producer (helper.ModelPriceHelper) for the search-unit rows so the
// pre-consume and settlement halves cannot drift apart unnoticed.

import (
	"fmt"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func priceGinCtx() *gin.Context {
	c, _ := newJSONContext("POST", "/", nil)
	return c
}

const meterModel = "model-a-rerank"

func seedSearchUnitPrice(t *testing.T, price string) {
	t.Helper()
	prev := ratio_setting.SearchUnitPrice2JSONString()
	if err := ratio_setting.UpdateSearchUnitPriceByJSONString(price); err != nil {
		t.Fatalf("seed search unit price: %v", err)
	}
	t.Cleanup(func() { _ = ratio_setting.UpdateSearchUnitPriceByJSONString(prev) })
}

func docs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = fmt.Sprintf("doc-%d", i)
	}
	return out
}

type meterResult struct {
	used int
	log  repo.Log
}

// settleRerank runs pre-consume pricing (when real) + postConsumeQuota for a
// rerank call and returns the billed quota and the consume-log row.
func settleRerank(t *testing.T, name string, nDocs int, usage *dto.Usage, upstreamSource string, tokenPrice bool) meterResult {
	t.Helper()
	u := &repo.User{Username: name, Quota: 100_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	info := newRelayInfo(u.Id, 0, 0)
	info.IsPlayground = true
	info.UserQuota = 100_000_000
	info.OriginModelName = meterModel
	info.RelayMode = relayconstant.RelayModeRerank
	info.RerankerInfo = &relaycommon.RerankerInfo{Documents: docs(nDocs)}
	info.UsageSource = upstreamSource
	if tokenPrice {
		info.PriceData = types.PriceData{ModelRatio: 2, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}
	} else {
		pd, err := helper.ModelPriceHelper(priceGinCtx(), info, 10, &types.TokenCountMeta{})
		if err != nil {
			t.Fatalf("ModelPriceHelper: %v", err)
		}
		info.PriceData = pd
	}
	c, _ := newJSONContext("POST", "/", nil)
	c.Set("token_name", "tkn")
	c.Set("username", name)
	postConsumeQuota(c, info, usage)

	var refreshed repo.User
	if err := repo.DB.First(&refreshed, u.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	var l repo.Log
	if err := repo.LOG_DB.Where("user_id = ?", u.Id).Order("id desc").First(&l).Error; err != nil {
		t.Fatalf("read consume log: %v", err)
	}
	return meterResult{used: refreshed.UsedQuota, log: l}
}

func TestRerankSettlement_SearchUnitMatrix(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	common.LogConsumeEnabled = true
	seedSearchUnitPrice(t, `{"`+meterModel+`":0.002}`)
	perUnit := int(0.002 * common.QuotaPerUnit) // 1000 at the default QuotaPerUnit

	for _, tc := range []struct {
		docs      int
		wantUnits int
	}{{1, 1}, {100, 1}, {101, 2}} {
		for _, usageCase := range []struct {
			name   string
			usage  *dto.Usage
			source string
			want   string
		}{
			{"upstream", &dto.Usage{TotalTokens: 5000}, "upstream", "upstream"},
			{"absent", nil, "", "estimated"},
		} {
			name := fmt.Sprintf("su-%d-%s", tc.docs, usageCase.name)
			t.Run(name, func(t *testing.T) {
				r := settleRerank(t, name, tc.docs, usageCase.usage, usageCase.source, false)
				// Token counts must not matter: 5000 upstream tokens bill the same
				// as an estimate, by units only.
				if want := perUnit * tc.wantUnits; r.used != want {
					t.Errorf("billed %d, want %d (%d units x %d)", r.used, want, tc.wantUnits, perUnit)
				}
				if r.log.UsageUnit != "search_unit" || r.log.UsageQuantity != int64(tc.wantUnits) {
					t.Errorf("unit/quantity = %q/%d, want search_unit/%d", r.log.UsageUnit, r.log.UsageQuantity, tc.wantUnits)
				}
				if r.log.UsageSource != usageCase.want {
					t.Errorf("source = %q, want %q", r.log.UsageSource, usageCase.want)
				}
				if r.log.RetrievalDocuments != tc.docs {
					t.Errorf("retrieval_documents = %d, want %d", r.log.RetrievalDocuments, tc.docs)
				}
			})
		}
	}
}

// Without a search-unit price the token ratio path is used and total_tokens
// (the only counter Jina reports) is the billable count.
func TestRerankSettlement_TokenPathWithoutSearchUnitPrice(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	common.LogConsumeEnabled = true
	seedSearchUnitPrice(t, `{}`)

	r := settleRerank(t, "tok-path", 101, &dto.Usage{TotalTokens: 50}, "upstream", true)
	if r.used != 100 { // 50 tokens x ModelRatio 2
		t.Errorf("billed %d, want 100 (50 tokens x ratio 2, independent of the 101 documents)", r.used)
	}
	if r.log.UsageUnit != "token" || r.log.UsageQuantity != 50 || r.log.UsageSource != "upstream" {
		t.Errorf("metering = %q/%d/%q, want token/50/upstream", r.log.UsageUnit, r.log.UsageQuantity, r.log.UsageSource)
	}
	if r.log.RetrievalDocuments != 101 {
		t.Errorf("retrieval_documents = %d, want 101", r.log.RetrievalDocuments)
	}
}

// An explicit 0 price is deliberately free; absent is not.
func TestRerankSettlement_ExplicitZeroIsFree_AbsentIsError(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	common.LogConsumeEnabled = true

	seedSearchUnitPrice(t, `{"`+meterModel+`":0}`)
	r := settleRerank(t, "zero-price", 3, &dto.Usage{TotalTokens: 10}, "upstream", false)
	if r.used != 0 || r.log.UsageUnit != "search_unit" {
		t.Errorf("explicit 0: billed %d unit %q, want 0 search_unit", r.used, r.log.UsageUnit)
	}

	if err := ratio_setting.UpdateSearchUnitPriceByJSONString(`{}`); err != nil {
		t.Fatal(err)
	}
	info := newRelayInfo(1, 0, 0)
	info.OriginModelName = "model-without-any-price"
	info.RelayMode = relayconstant.RelayModeRerank
	info.RerankerInfo = &relaycommon.RerankerInfo{Documents: docs(2)}
	if _, err := helper.ModelPriceHelper(priceGinCtx(), info, 10, &types.TokenCountMeta{}); err == nil {
		t.Error("a model with neither search unit price nor token ratio must still be rejected, not priced as free")
	}
}

func TestEmbeddingsSettlement_RecordsRetrievalDocuments(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	common.LogConsumeEnabled = true

	u := &repo.User{Username: "emb", Quota: 1_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	info := newRelayInfo(u.Id, 0, 0)
	info.IsPlayground = true
	info.OriginModelName = "model-b-embed"
	info.RelayMode = relayconstant.RelayModeEmbeddings
	info.Request = &dto.EmbeddingRequest{Input: []any{"a", "b", "c"}}
	info.PriceData = types.PriceData{ModelRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}
	c, _ := newJSONContext("POST", "/", nil)
	c.Set("username", "emb")
	postConsumeQuota(c, info, &dto.Usage{PromptTokens: 12, TotalTokens: 12})

	var l repo.Log
	if err := repo.LOG_DB.Where("user_id = ?", u.Id).First(&l).Error; err != nil {
		t.Fatal(err)
	}
	if l.RetrievalDocuments != 3 || l.UsageUnit != "token" || l.UsageQuantity != 12 || l.UsageSource != "upstream" {
		t.Errorf("embeddings metering = docs %d unit %q qty %d src %q, want 3/token/12/upstream",
			l.RetrievalDocuments, l.UsageUnit, l.UsageQuantity, l.UsageSource)
	}
}
