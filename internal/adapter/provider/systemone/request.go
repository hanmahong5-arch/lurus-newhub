package systemone

import (
	"encoding/json"
	"errors"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// upstreamRequest is the body sent upstream. The hosted API takes exactly
// {state, model, questions}; the optional extras only exist for self-hosted
// servers and are left nil (and so omitted) for the hosted channel.
type upstreamRequest struct {
	State         json.RawMessage                  `json:"state"`
	Model         string                           `json:"model"`
	Questions     map[string]dto.SystemOneQuestion `json:"questions"`
	Lang          *string                          `json:"lang,omitempty"`
	MinConfidence *float64                         `json:"min_confidence,omitempty"`
	MaxLen        *int                             `json:"max_len,omitempty"`
	HeadMaxLen    *int                             `json:"head_max_len,omitempty"`
}

// ConvertSystemOneRequest builds the upstream body. Rebuilding it (instead of
// forwarding the caller's struct) is what keeps self-hosted-only knobs away
// from the hosted API, which validates strictly and would reject them.
func (a *Adaptor) ConvertSystemOneRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.SystemOneRequest) (any, error) {
	if req == nil {
		return nil, errors.New("systemone adaptor: request is nil")
	}
	if info == nil {
		return nil, errors.New("systemone adaptor: relay info is nil")
	}
	model := info.UpstreamModelName
	if model == "" {
		model = req.Model
	}
	out := &upstreamRequest{State: req.State, Model: model}

	if info.ChannelType == constant.ChannelTypeSystemOneCompatible {
		out.Questions = req.Questions
		out.Lang, out.MinConfidence, out.MaxLen, out.HeadMaxLen = req.Lang, req.MinConfidence, req.MaxLen, req.HeadMaxLen
		return out, nil
	}

	// Hosted: only the fields the hosted API documents, per question too.
	out.Questions = make(map[string]dto.SystemOneQuestion, len(req.Questions))
	for id, q := range req.Questions {
		out.Questions[id] = dto.SystemOneQuestion{Type: q.Type, Instructions: q.Instructions, Criteria: q.Criteria}
	}
	return out, nil
}
