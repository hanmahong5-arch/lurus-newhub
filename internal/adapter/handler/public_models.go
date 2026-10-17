package handler

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// ListPublicModels serves GET /api/v1/models: the public model catalogue in
// the shape OpenRouter publishes at the same path, so that tools which already
// read OpenRouter's list (pricing per token, architecture, endpoints) can read
// ours without a Lurus-specific parser, and so a developer can see what a call
// costs before creating an account.
//
// It publishes the same rows as /api/pricing and /api/v2/switch/pricing — the
// platform-shared catalogue, priced rows only (repo.PricedOnly) — and adds no
// new data, only the conversion the client would otherwise have to do:
//
//	USD per token = model_ratio × group_ratio / QuotaPerUnit
//	(completion: × completion_ratio; cache read: × cache_ratio;
//	 cache write: × create_cache_ratio)
//
// The group a row is priced at is "default" when the model is served there,
// otherwise the cheapest group an anonymous caller may use; it is named in
// lurus.group, because a caller in a different group pays that group's ratio.
// context_length is null: the gateway does not record it, and an invented
// number is worse than none.
//
// No auth, read-only, no migration.
func ListPublicModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": publicModelCatalogue()})
}

// GetPublicModel serves GET /api/v1/models/*model — one row of the catalogue,
// 404 when the model is not in the public catalogue. A catch-all, because
// model names may contain "/" (openai/gpt-oss-120b).
func GetPublicModel(c *gin.Context) {
	id := strings.TrimPrefix(c.Param("model"), "/")
	for _, m := range publicModelCatalogue() {
		if m.ID == id {
			c.JSON(http.StatusOK, gin.H{"data": m})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
		"message": "model not found in the public catalogue",
		"code":    404,
	}})
}

type publicModel struct {
	ID            string                `json:"id"`
	CanonicalSlug string                `json:"canonical_slug"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	ContextLength *int                  `json:"context_length"`
	Architecture  publicModelArch       `json:"architecture"`
	Pricing       publicModelPricing    `json:"pricing"`
	Lurus         publicModelLurusExtra `json:"lurus"`
}

type publicModelArch struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

// publicModelPricing is USD per unit, as decimal strings (OpenRouter's wire
// form, which avoids float rendering such as 2e-06). Prompt/completion are
// per token, request is per call; cache fields are omitted when the model has
// no cache price.
type publicModelPricing struct {
	Prompt          string `json:"prompt"`
	Completion      string `json:"completion"`
	Request         string `json:"request"`
	InputCacheRead  string `json:"input_cache_read,omitempty"`
	InputCacheWrite string `json:"input_cache_write,omitempty"`
}

type publicModelLurusExtra struct {
	Vendor                 string   `json:"vendor,omitempty"`
	Group                  string   `json:"group"`
	GroupRatio             float64  `json:"group_ratio"`
	Modality               string   `json:"modality,omitempty"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
	// CNY per million tokens (per call for fixed-price models): what the
	// platform wallet is actually debited, at the configured USD rate.
	PromptCNYPerMTok     float64 `json:"prompt_cny_per_mtok"`
	CompletionCNYPerMTok float64 `json:"completion_cny_per_mtok"`
	RequestCNY           float64 `json:"request_cny"`
}

func publicModelCatalogue() []publicModel {
	rows := repo.PricedOnly(repo.GetPricingForTenant(""))

	vendorByID := make(map[int]string)
	for _, v := range repo.GetVendors() {
		vendorByID[v.ID] = v.Name
	}
	usable := app.GetUserUsableGroups("")

	out := make([]publicModel, 0, len(rows))
	for _, p := range rows {
		group, groupRatio, ok := publicPriceGroup(p.EnableGroup, usable)
		if !ok {
			// Served only in groups an anonymous caller cannot pick:
			// no price we could publish would be one they can pay.
			continue
		}

		var promptUSD, completionUSD, requestUSD float64
		if p.QuotaType == 1 {
			requestUSD = p.ModelPrice * groupRatio
		} else {
			promptUSD = p.ModelRatio * groupRatio / common.QuotaPerUnit
			completionUSD = promptUSD * p.CompletionRatio
		}

		pricing := publicModelPricing{
			Prompt:     usdString(promptUSD),
			Completion: usdString(completionUSD),
			Request:    usdString(requestUSD),
		}
		if p.QuotaType != 1 {
			if r, found := ratio_setting.GetCacheRatio(p.ModelName); found {
				pricing.InputCacheRead = usdString(promptUSD * r)
			}
			if r, found := ratio_setting.GetCreateCacheRatio(p.ModelName); found {
				pricing.InputCacheWrite = usdString(promptUSD * r)
			}
		}

		endpoints := make([]string, 0, len(p.SupportedEndpointTypes))
		for _, e := range p.SupportedEndpointTypes {
			endpoints = append(endpoints, string(e))
		}

		in, outM, modality := publicArchitecture(p.Modality)
		out = append(out, publicModel{
			ID:            p.ModelName,
			CanonicalSlug: p.ModelName,
			Name:          p.ModelName,
			Description:   p.Description,
			Architecture: publicModelArch{
				Modality:         modality,
				InputModalities:  in,
				OutputModalities: outM,
			},
			Pricing: pricing,
			Lurus: publicModelLurusExtra{
				Vendor:                 vendorByID[p.VendorID],
				Group:                  group,
				GroupRatio:             groupRatio,
				Modality:               p.Modality,
				SupportedEndpointTypes: endpoints,
				PromptCNYPerMTok:       usdToCNYPerMTok(promptUSD),
				CompletionCNYPerMTok:   usdToCNYPerMTok(completionUSD),
				RequestCNY:             usdToCNY(requestUSD),
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// publicPriceGroup picks the group a public row is priced at: "default" when
// the model is served there and anonymous callers may use it, else the
// cheapest such group (ties broken by name, so the answer is stable).
func publicPriceGroup(enabled []string, usable map[string]string) (string, float64, bool) {
	best, bestRatio, found := "", 0.0, false
	for _, g := range enabled {
		if _, ok := usable[g]; !ok {
			continue
		}
		r := ratio_setting.GetGroupRatio(g)
		if g == "default" {
			return g, r, true
		}
		if !found || r < bestRatio || (r == bestRatio && g < best) {
			best, bestRatio, found = g, r, true
		}
	}
	return best, bestRatio, found
}

// publicArchitecture maps the gateway's single modality word onto
// OpenRouter's input/output modality lists. Unknown or empty modality is
// reported as text→text only for chat-shaped rows; otherwise left empty.
func publicArchitecture(m string) (in, out []string, modality string) {
	switch m {
	case "", "chat", "decision":
		return []string{"text"}, []string{"text"}, "text->text"
	case "image":
		return []string{"text"}, []string{"image"}, "text->image"
	case "embedding":
		return []string{"text"}, []string{"embeddings"}, "text->embeddings"
	case "rerank":
		return []string{"text"}, []string{"text"}, "text->text"
	case "audio":
		return []string{"audio", "text"}, []string{"audio", "text"}, "audio+text->audio+text"
	default:
		return []string{}, []string{}, ""
	}
}

func usdString(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// usdToCNY converts through the same rate the wallet debit uses
// (USD → quota → yuan via currency.LucToLut, as currency.QuotaToCNY does,
// without its integer truncation), so the published yuan price is the
// charged one. Rounded to 6 decimals for display.
func usdToCNY(usd float64) float64 {
	return math.Round(currency.USDToCNY(usd)*1e6) / 1e6
}

func usdToCNYPerMTok(usdPerToken float64) float64 {
	return usdToCNY(usdPerToken * 1e6)
}
