package routingdecision

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// Wire formats decision routing understands. Anything else is skipped before
// any policy work: the evaluator reads plain-text intent, and only these two
// formats carry it in a shape we can read without guessing.
const (
	pathChat      = "/v1/chat/completions"
	pathResponses = "/v1/responses"
)

// SupportedPath reports whether decision routing applies to the request path
// at all (OpenAI chat completions or Responses). Other paths are skipped
// silently - not "ineligible", just not this feature's business.
func SupportedPath(path string) bool {
	p := strings.TrimRight(path, "/")
	return p == pathChat || p == pathResponses
}

// MaxUserTextBytes caps the user text sent to the evaluator. The evaluator
// only needs the gist of the intent; a long prompt would make the evaluation
// slower and dearer than the request it is meant to optimise.
const MaxUserTextBytes = 8192

// Why values name the guard that made a request ineligible. They go into the
// audit details and logs, never to the caller.
const (
	WhyBody          = "unreadable_body"
	WhyMultiTurn     = "not_first_turn"
	WhyTools         = "tools"
	WhyPrevResponse  = "previous_response_id"
	WhyAffinity      = "session_affinity"
	WhyReasoning     = "reasoning"
	WhyMetadataPin   = "metadata_pin"
	WhyMetadataAudit = "metadata_audit"
	WhyNoUserText    = "no_user_text"
	WhyNonText       = "non_text_content"
)

// Eligibility is the verdict of Check.
type Eligibility struct {
	OK       bool
	Why      string // set when !OK
	UserText string // set when OK: the last user message, truncated to MaxUserTextBytes
}

// Flags carries request facts the body alone does not show.
type Flags struct {
	// SessionAffinity is true when the caller supplied any conversation key
	// (X-Session-Id, prompt_cache_key, metadata.user_id). A conversation that
	// is pinned to a channel must not hop models between turns.
	SessionAffinity bool
}

// rawRequest is the subset of the OpenAI chat / Responses bodies the guard
// reads. Everything is json.RawMessage or loosely typed on purpose: the guard
// must classify a malformed-but-parseable body as ineligible, never panic or
// reject it - the relay proper owns request validation.
type rawRequest struct {
	Model              string          `json:"model"`
	Messages           []rawMessage    `json:"messages"`
	Input              json.RawMessage `json:"input"`
	Tools              json.RawMessage `json:"tools"`
	ToolChoice         json.RawMessage `json:"tool_choice"`
	Functions          json.RawMessage `json:"functions"`
	FunctionCall       json.RawMessage `json:"function_call"`
	PreviousResponseID json.RawMessage `json:"previous_response_id"`
	ReasoningEffort    json.RawMessage `json:"reasoning_effort"`
	Reasoning          json.RawMessage `json:"reasoning"`
	Metadata           json.RawMessage `json:"metadata"`
}

type rawMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
	// Type is set on Responses input items ("message", "function_call", ...).
	Type string `json:"type"`
}

// Check decides whether a request may be routed by decision, and extracts the
// text to evaluate. Every denied case maps to one of the documented guards
// (first turn only, no tool use, no stored/linked conversation, no reasoning
// models, no pinned/audited traffic): a rewrite that changes the model under
// an in-flight tool loop or an audited run would be a correctness or
// compliance bug, so the guard errs on the side of ineligible.
func Check(path string, body []byte, f Flags) Eligibility {
	var r rawRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return Eligibility{Why: WhyBody}
	}
	if present(r.Tools) || present(r.ToolChoice) || present(r.Functions) || present(r.FunctionCall) {
		return Eligibility{Why: WhyTools}
	}
	if present(r.PreviousResponseID) && !isEmptyString(r.PreviousResponseID) {
		return Eligibility{Why: WhyPrevResponse}
	}
	if f.SessionAffinity {
		return Eligibility{Why: WhyAffinity}
	}
	if (present(r.ReasoningEffort) && !isEmptyString(r.ReasoningEffort)) || reasoningObjectSet(r.Reasoning) || IsReasoningModelName(r.Model) {
		return Eligibility{Why: WhyReasoning}
	}
	if why := metadataGuard(r.Metadata); why != "" {
		return Eligibility{Why: why}
	}

	var text string
	var why string
	if strings.TrimRight(path, "/") == pathResponses {
		text, why = lastUserTextResponses(r.Input)
	} else {
		text, why = lastUserTextChat(r.Messages)
	}
	if why != "" {
		return Eligibility{Why: why}
	}
	if strings.TrimSpace(text) == "" {
		return Eligibility{Why: WhyNoUserText}
	}
	return Eligibility{OK: true, UserText: truncateUTF8(text, MaxUserTextBytes)}
}

// present reports a JSON field that is set to something other than null /
// empty array / empty object. `"tools": []` is how some SDKs say "no tools".
func present(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	switch string(t) {
	case "", "null", "[]", "{}":
		return false
	}
	return true
}

func isEmptyString(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	return s == ""
}

// reasoningObjectSet: the Responses `reasoning` object counts only when it
// asks for something (effort / summary / generate_summary).
func reasoningObjectSet(raw json.RawMessage) bool {
	if !present(raw) {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return true // not an object: unknown shape, be conservative
	}
	for _, v := range m {
		if present(v) && !isEmptyString(v) {
			return true
		}
	}
	return false
}

// reasoningNameRe matches the naming conventions of reasoning models without
// listing any vendor's models: an "o<digits>" family prefix, an "r<digits>"
// token, or a name that says what it is. It is deliberately about the
// requested name only; a policy on a virtual public model carries no
// reasoning behaviour of its own.
var reasoningNameRe = regexp.MustCompile(`(^|[-_/])(o\d+|r\d+)($|[-_])|thinking|reasoner|reasoning`)

// IsReasoningModelName is a name-based check for reasoning models, whose
// behaviour depends on hidden state and per-model reasoning parameters that a
// transparent rewrite would silently change.
func IsReasoningModelName(model string) bool {
	return reasoningNameRe.MatchString(strings.ToLower(model))
}

// metadataGuard denies requests whose metadata marks them as pinned to a
// route ("pin:<x>") or as audited ("audit:true"). Checked on keys and on
// string values, because clients encode both ways.
func metadataGuard(raw json.RawMessage) string {
	if !present(raw) {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for k, v := range m {
		lk := strings.ToLower(strings.TrimSpace(k))
		sv, _ := v.(string)
		lv := strings.ToLower(strings.TrimSpace(sv))
		if strings.HasPrefix(lk, "pin:") || strings.HasPrefix(lv, "pin:") {
			return WhyMetadataPin
		}
		if lk == "audit:true" || lv == "audit:true" {
			return WhyMetadataAudit
		}
		if lk == "audit" {
			if b, ok := v.(bool); (ok && b) || lv == "true" {
				return WhyMetadataAudit
			}
		}
	}
	return ""
}

// lastUserTextChat accepts only a first-turn chat conversation: system /
// developer messages followed by user messages, no assistant, tool or function
// history. Returns the LAST user message when it is plain text.
func lastUserTextChat(msgs []rawMessage) (string, string) {
	last := -1
	for i, m := range msgs {
		switch strings.ToLower(m.Role) {
		case "system", "developer":
		case "user":
			last = i
		default: // assistant, tool, function, anything else = a later turn
			return "", WhyMultiTurn
		}
		if present(m.ToolCalls) {
			return "", WhyMultiTurn
		}
	}
	if last < 0 {
		return "", WhyNoUserText
	}
	return plainText(msgs[last].Content, "text")
}

// lastUserTextResponses is the Responses-format twin: `input` is a string, or
// an array of items. Any item that is not a user message (assistant output,
// function_call, function_call_output, reasoning, item references...) marks a
// later turn.
func lastUserTextResponses(input json.RawMessage) (string, string) {
	t := bytes.TrimSpace(input)
	if len(t) == 0 || string(t) == "null" {
		return "", WhyNoUserText
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) != nil {
			return "", WhyBody
		}
		return s, ""
	}
	var items []rawMessage
	if json.Unmarshal(t, &items) != nil {
		return "", WhyBody
	}
	last := -1
	for i, it := range items {
		if it.Type != "" && it.Type != "message" {
			return "", WhyMultiTurn
		}
		switch strings.ToLower(it.Role) {
		case "system", "developer":
		case "user":
			last = i
		default:
			return "", WhyMultiTurn
		}
	}
	if last < 0 {
		return "", WhyNoUserText
	}
	return plainText(items[last].Content, "input_text")
}

// plainText extracts text from a message content that is a string, or an array
// whose every part is a text part of the given type. Any other part (image,
// audio, file) makes the message non-text: the evaluator cannot judge it.
func plainText(content json.RawMessage, partType string) (string, string) {
	t := bytes.TrimSpace(content)
	if len(t) == 0 || string(t) == "null" {
		return "", WhyNoUserText
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) != nil {
			return "", WhyBody
		}
		return s, ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(t, &parts) != nil {
		return "", WhyNonText
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type != partType && p.Type != "text" {
			return "", WhyNonText
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String(), ""
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
