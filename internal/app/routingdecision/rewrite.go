package routingdecision

import (
	"bytes"
	"encoding/json"
	"errors"
)

// RewriteModel returns body with its top-level "model" replaced by model.
// Every other field is carried over byte-for-byte as raw JSON (numbers keep
// their exact text, nested documents are untouched); only top-level key order
// is not preserved, which JSON consumers do not depend on.
func RewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("routing: request body is not a JSON object")
	}
	name, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = name
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // user prompts pass through verbatim, including < > &
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
