package dto

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// SystemOneRequest is the body of POST /v1/systemone (TypeSafe wire). The
// optional pointer extras are only meaningful to self-hosted compatible
// servers; pointers keep an explicit zero (min_confidence: 0) distinguishable
// from "not sent". Unknown top-level fields are not modelled, so a re-marshal
// drops them.
type SystemOneRequest struct {
	State         json.RawMessage              `json:"state"` // string, object or array
	Model         string                       `json:"model"`
	Questions     map[string]SystemOneQuestion `json:"questions"`
	Lang          *string                      `json:"lang,omitempty"`
	MinConfidence *float64                     `json:"min_confidence,omitempty"`
	MaxLen        *int                         `json:"max_len,omitempty"`
	HeadMaxLen    *int                         `json:"head_max_len,omitempty"`
}

// SystemOneQuestion is one question; the shape of instructions/criteria
// depends on Type (noul/choice/score), so they stay raw and nothing is lost.
type SystemOneQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions,omitempty"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
	Labels       json.RawMessage `json:"labels,omitempty"`       // self-hosted noul only
	OptionOrder  json.RawMessage `json:"option_order,omitempty"` // self-hosted only
}

func (r *SystemOneRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *SystemOneRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

// GetTokenCountMeta joins the state with every question's instructions and
// criteria, questions in id order so the pre-consume estimate is stable.
func (r *SystemOneRequest) GetTokenCountMeta() *types.TokenCountMeta {
	ids := make([]string, 0, len(r.Questions))
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	texts := []string{rawJSONText(r.State)}
	for _, id := range ids {
		q := r.Questions[id]
		texts = append(texts, rawJSONText(q.Instructions), rawJSONText(q.Criteria))
	}
	nonEmpty := texts[:0]
	for _, t := range texts {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	return &types.TokenCountMeta{CombineText: strings.Join(nonEmpty, "\n")}
}

// rawJSONText is the text a JSON value contributes to token estimation: a
// string unquoted, null as nothing, anything structured as its JSON text.
func rawJSONText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if string(raw) == "null" {
		return ""
	}
	return string(raw)
}

// SystemOneResponse is the body returned to the caller. Answers are kept as
// received so per-answer extras (e.g. answer_confidence) pass through.
type SystemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   SystemOneUsage             `json:"usage"`
}

type SystemOneUsage struct {
	InputTokens  int  `json:"input_tokens"`
	OutputTokens int  `json:"output_tokens"`
	Truncated    bool `json:"truncated,omitempty"`
}
