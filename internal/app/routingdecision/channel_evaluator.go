package routingdecision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/systemone"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	pkgcommon "github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// questionID is the single question id of every evaluation.
const questionID = "route"

// defaultInstructions is used when the policy carries none.
const defaultInstructions = "Pick the option that best matches what the user is asking for. " +
	"Answer no_preference when none of the options clearly fits."

const noPreferenceCriteria = "None of the other options clearly fits, or the request is ambiguous."

// ChannelEvaluator evaluates through the existing System One channels: the
// evaluator model is selected like any routed model (same tenant scoping,
// weights, cooldowns), and the call is made directly - not through the relay
// handler - so it is billed and logged separately and can never recurse into
// decision routing.
type ChannelEvaluator struct{}

// BuildQuestion renders the routing question. Candidates are keyed by id; the
// no_preference option is appended so the evaluator has an honest way out.
func BuildQuestion(instructions string, cands []entity.RoutingCandidate) (json.RawMessage, json.RawMessage, error) {
	if instructions == "" {
		instructions = defaultInstructions
	}
	crit := make(map[string]string, len(cands)+1)
	for _, c := range cands {
		crit[c.ID] = c.Criteria
	}
	crit[NoPreferenceID] = noPreferenceCriteria
	ins, err := json.Marshal(instructions)
	if err != nil {
		return nil, nil, err
	}
	cr, err := json.Marshal(crit)
	if err != nil {
		return nil, nil, err
	}
	return ins, cr, nil
}

// Evaluate implements Evaluator.
func (ChannelEvaluator) Evaluate(ctx context.Context, c *gin.Context, in EvalInput) (*EvalOutput, error) {
	sc, w := scratchContext(ctx, c)

	group := pkgcommon.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	channel, selGroup, err := app.CacheGetRandomSatisfiedChannel(&app.RetryParam{ //nolint:contextcheck // RetryParam.Ctx carries the scratch gin context; the selector has no ctx parameter
		Ctx:        sc,
		ModelName:  in.Model,
		TokenGroup: group,
		TenantID:   in.TenantID,
		Retry:      pkgcommon.GetPointer(0),
	})
	if err != nil || channel == nil {
		return nil, ErrNoEvaluatorChannel
	}
	if channel.Type != constant.ChannelTypeTypeSafe && channel.Type != constant.ChannelTypeSystemOneCompatible {
		return nil, fmt.Errorf("routing: evaluator model %q is served by a channel that does not speak System One", in.Model)
	}
	key, _, apiErr := app.SelectKeyWithAffinity(ctx, channel, "")
	if apiErr != nil {
		return nil, fmt.Errorf("routing: evaluator channel key: %w", apiErr)
	}

	upstreamModel := mapModel(channel.GetModelMapping(), in.Model)
	info := &common.RelayInfo{
		OriginModelName: in.Model,
		ChannelMeta: &common.ChannelMeta{
			ChannelType:       channel.Type,
			ChannelId:         channel.Id,
			ChannelBaseUrl:    channel.GetBaseURL(),
			ApiKey:            key,
			UpstreamModelName: upstreamModel,
			ChannelSetting:    channel.GetSetting(),
		},
	}

	ins, crit, err := BuildQuestion(in.Instructions, in.Candidates)
	if err != nil {
		return nil, err
	}
	state, err := json.Marshal(in.Text)
	if err != nil {
		return nil, err
	}
	sreq := &dto.SystemOneRequest{
		State: state,
		Model: in.Model,
		Questions: map[string]dto.SystemOneQuestion{
			questionID: {Type: "choice", Instructions: ins, Criteria: crit},
		},
	}
	ad := &systemone.Adaptor{}
	converted, err := ad.ConvertSystemOneRequest(sc, info, sreq)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(converted)
	if err != nil {
		return nil, err
	}
	url, err := ad.GetRequestURL(info)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if err := ad.SetupRequestHeader(sc, &req.Header, info); err != nil {
		return nil, err
	}
	client, err := app.GetHttpClientFor(info.ChannelSetting.Proxy, info.ChannelSetting.ForceHTTP1)
	if err != nil || client == nil {
		return nil, errors.New("routing: no HTTP client available for the evaluator channel")
	}
	resp, err := client.Do(req) // #nosec G704 -- URL derives from an SSRF-validated channel base_url, dial-guarded like the relay client.
	if err != nil {
		return nil, fmt.Errorf("routing: evaluator request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("routing: evaluator upstream answered status %d", resp.StatusCode)
	}
	if _, apiErr := ad.DoResponse(sc, resp, info); apiErr != nil {
		return nil, fmt.Errorf("routing: evaluator reply unusable: %w", apiErr)
	}
	out, err := parseAnswer(w.body.Bytes())
	if err != nil {
		return nil, err
	}
	out.ChannelID, out.ChannelType, out.Group, out.UpstreamModel = channel.Id, channel.Type, selGroup, upstreamModel
	return out, nil
}

// parseAnswer reads the normalised System One reply (DoResponse's output).
func parseAnswer(body []byte) (*EvalOutput, error) {
	var r struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
			Confidence    *float64           `json:"confidence"`
		} `json:"answers"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("routing: evaluator reply is not valid JSON")
	}
	a, ok := r.Answers[questionID]
	if !ok || a.Type != "choice" || a.Choice == "" {
		return nil, errors.New("routing: evaluator reply has no choice answer")
	}
	probs := a.Probabilities
	if probs == nil {
		probs = map[string]float64{}
	}
	if _, has := probs[a.Choice]; !has {
		if a.Confidence == nil {
			return nil, errors.New("routing: evaluator reply carries no probability for its choice")
		}
		probs[a.Choice] = *a.Confidence
	}
	return &EvalOutput{
		Choice:        a.Choice,
		Probabilities: probs,
		InputTokens:   r.Usage.InputTokens,
		OutputTokens:  r.Usage.OutputTokens,
	}, nil
}

// mapModel applies a channel's model_mapping (single hop is what an evaluator
// channel needs; chains are followed with a cycle guard).
func mapModel(mapping, model string) string {
	if mapping == "" || mapping == "{}" {
		return model
	}
	var m map[string]string
	if json.Unmarshal([]byte(mapping), &m) != nil {
		return model
	}
	cur := model
	seen := map[string]bool{cur: true}
	for {
		next, ok := m[cur]
		if !ok || next == "" || seen[next] {
			return cur
		}
		seen[next] = true
		cur = next
	}
}

// captureWriter collects what adaptor.DoResponse writes to its gin context.
type captureWriter struct {
	h    http.Header
	body bytes.Buffer
	code int
}

func (w *captureWriter) Header() http.Header         { return w.h }
func (w *captureWriter) WriteHeader(code int)        { w.code = code }
func (w *captureWriter) Write(b []byte) (int, error) { return w.body.Write(b) }

// scratchContext is a throwaway gin context for the evaluator's channel
// selection and response normalisation. It deliberately shares NOTHING with
// the caller's request except the identity of the tenant's groups: selection
// predicates read per-request keys (modality, provider filter, already-tried
// channels, session affinity) and the caller's request must not constrain or
// be mutated by the evaluator's own routing.
func scratchContext(ctx context.Context, c *gin.Context) (*gin.Context, *captureWriter) {
	w := &captureWriter{h: http.Header{}}
	sc, _ := gin.CreateTestContext(w)
	sc.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/v1/systemone", nil)
	for _, k := range []constant.ContextKey{constant.ContextKeyUserGroup, constant.ContextKeyUsingGroup} {
		if v, ok := pkgcommon.GetContextKey(c, k); ok {
			pkgcommon.SetContextKey(sc, k, v)
		}
	}
	return sc, w
}
