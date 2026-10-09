package contentpolicy

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Format names the request wire format a body is in.
type Format string

const (
	FormatOpenAIChat Format = "openai_chat"
	FormatResponses  Format = "responses"
	FormatClaude     Format = "claude"
	FormatGemini     Format = "gemini"
)

// Hit aggregates one rule's matches over a request.
type Hit struct {
	RuleId  int64
	Kind    string
	Mode    string
	Builtin string
	Count   int
}

// Rejection names the enforce-mode reject rule that stopped a request. It
// carries the rule id only, never the matched text.
type Rejection struct {
	RuleId int64
}

// Result is the outcome of applying a Ruleset to a request body.
type Result struct {
	// Body is the (possibly rewritten) body. It is the input slice itself when
	// nothing changed.
	Body     []byte
	Changed  bool
	Rejected *Rejection
	Hits     []Hit
}

// slot is one piece of text in a request body, addressed by its sjson path.
type slot struct {
	path string
	role string
	text string
}

// Apply runs the rules, in order, over every text slot of body. It works on
// the raw bytes with path-addressed edits so unrelated fields (tool schemas,
// media, unknown vendor extensions) are forwarded byte-for-byte; this is also
// why callers must hand the SAME bytes to the parsed and the pass-through
// paths: masking the typed struct only would let pass-through forward the
// original body untouched.
func (rs *Ruleset) Apply(format Format, body []byte) Result {
	res := Result{Body: body}
	if rs.Len() == 0 || !gjson.ValidBytes(body) {
		return res
	}
	slots := collectSlots(format, body)
	if len(slots) == 0 {
		return res
	}
	hits := make(map[int64]*Hit)
	var order []int64
	record := func(r *compiledRule, n int) {
		h, ok := hits[r.Id]
		if !ok {
			h = &Hit{RuleId: r.Id, Kind: r.Kind, Mode: r.Mode, Builtin: r.Builtin}
			hits[r.Id] = h
			order = append(order, r.Id)
		}
		h.Count += n
	}
	out := body
	for _, sl := range slots {
		text := sl.text
		changed := false
		for i := range rs.rules {
			r := &rs.rules[i]
			if !roleMatches(r.RoleScope, sl.role) {
				continue
			}
			spans := r.find(text)
			if len(spans) == 0 {
				continue
			}
			record(r, len(spans))
			if r.Mode != ModeEnforce {
				continue
			}
			if r.Kind == KindReject {
				res.Rejected = &Rejection{RuleId: r.Id}
				res.Hits = collectHits(hits, order)
				// The caller refuses the request; no body is forwarded.
				return res
			}
			text = maskSpans(text, spans, r.repl)
			changed = true
		}
		if changed {
			next, err := sjson.SetBytes(out, sl.path, text)
			if err == nil {
				out = next
				res.Changed = true
			}
		}
	}
	res.Body = out
	res.Hits = collectHits(hits, order)
	return res
}

func collectHits(m map[int64]*Hit, order []int64) []Hit {
	out := make([]Hit, 0, len(order))
	for _, id := range order {
		out = append(out, *m[id])
	}
	return out
}

func collectSlots(format Format, body []byte) []slot {
	root := gjson.ParseBytes(body)
	var out []slot
	add := func(path, role, text string) {
		if text != "" {
			out = append(out, slot{path: path, role: role, text: text})
		}
	}
	switch format {
	case FormatOpenAIChat:
		collectMessages(root, "messages", add, false)
		collectTextValue(root.Get("prompt"), "prompt", RoleUser, add)
	case FormatClaude:
		collectTextValue(root.Get("system"), "system", RoleSystem, add)
		collectMessages(root, "messages", add, true)
	case FormatResponses:
		collectTextValue(root.Get("instructions"), "instructions", RoleSystem, add)
		in := root.Get("input")
		if in.Type == gjson.String {
			add("input", RoleUser, in.String())
		} else if in.IsArray() {
			in.ForEach(func(k, item gjson.Result) bool {
				base := "input." + k.String()
				if item.Type == gjson.String {
					add(base, RoleUser, item.String())
					return true
				}
				role := normalizeRole(item.Get("role").String())
				collectTextValue(item.Get("content"), base+".content", role, add)
				return true
			})
		}
	case FormatGemini:
		for _, key := range []string{"systemInstruction", "system_instruction"} {
			collectGeminiParts(root.Get(key).Get("parts"), key+".parts", RoleSystem, add)
		}
		root.Get("contents").ForEach(func(k, item gjson.Result) bool {
			role := RoleUser
			if item.Get("role").String() == "model" {
				role = RoleAssistant
			}
			collectGeminiParts(item.Get("parts"), "contents."+k.String()+".parts", role, add)
			return true
		})
	}
	return out
}

func collectGeminiParts(parts gjson.Result, base, role string, add func(path, role, text string)) {
	parts.ForEach(func(k, p gjson.Result) bool {
		if t := p.Get("text"); t.Type == gjson.String {
			add(base+"."+k.String()+".text", role, t.String())
		}
		return true
	})
}

// collectMessages walks an OpenAI/Anthropic style messages array.
func collectMessages(root gjson.Result, key string, add func(path, role, text string), toolResults bool) {
	root.Get(key).ForEach(func(k, m gjson.Result) bool {
		role := normalizeRole(m.Get("role").String())
		base := key + "." + k.String() + ".content"
		content := m.Get("content")
		if content.Type == gjson.String {
			add(base, role, content.String())
			return true
		}
		content.ForEach(func(pk, part gjson.Result) bool {
			pbase := base + "." + pk.String()
			if t := part.Get("text"); t.Type == gjson.String {
				add(pbase+".text", role, t.String())
			}
			if toolResults && part.Get("type").String() == "tool_result" {
				collectTextValue(part.Get("content"), pbase+".content", role, add)
			}
			return true
		})
		return true
	})
}

// collectTextValue handles a field that is either a string or an array of
// strings / text parts.
func collectTextValue(v gjson.Result, path, role string, add func(path, role, text string)) {
	switch {
	case v.Type == gjson.String:
		add(path, role, v.String())
	case v.IsArray():
		v.ForEach(func(k, e gjson.Result) bool {
			p := path + "." + k.String()
			if e.Type == gjson.String {
				add(p, role, e.String())
			} else if t := e.Get("text"); t.Type == gjson.String {
				add(p+".text", role, t.String())
			}
			return true
		})
	}
}

func normalizeRole(r string) string {
	switch r {
	case "system", "developer":
		return RoleSystem
	case "assistant", "model":
		return RoleAssistant
	case "user", "":
		return RoleUser
	default:
		// tool / function / unknown roles only match role_scope=any.
		return "other:" + r
	}
}
