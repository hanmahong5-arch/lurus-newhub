package dto

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestImageRequest_UnmarshalJSON_ExtractsExtraFields(t *testing.T) {
	raw := `{
		"model": "dall-e-3",
		"prompt": "a cat",
		"n": 2,
		"size": "1024x1024",
		"unknown_vendor_field": "vendor-value",
		"another_extra": 123
	}`
	var img ImageRequest
	if err := json.Unmarshal([]byte(raw), &img); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if img.Model != "dall-e-3" || img.Prompt != "a cat" || img.N != 2 || img.Size != "1024x1024" {
		t.Fatalf("known fields not parsed correctly: %+v", img)
	}
	if len(img.Extra) != 2 {
		t.Fatalf("Extra = %+v, want 2 entries", img.Extra)
	}
	if string(img.Extra["unknown_vendor_field"]) != `"vendor-value"` {
		t.Fatalf("Extra[unknown_vendor_field] = %s", img.Extra["unknown_vendor_field"])
	}
	if string(img.Extra["another_extra"]) != "123" {
		t.Fatalf("Extra[another_extra] = %s", img.Extra["another_extra"])
	}
	// Known fields must not leak into Extra.
	if _, ok := img.Extra["model"]; ok {
		t.Fatal("known field 'model' leaked into Extra map")
	}
}

func TestImageRequest_UnmarshalJSON_NoExtraFieldsYieldsEmptyMap(t *testing.T) {
	raw := `{"model":"dall-e-2","prompt":"a dog"}`
	var img ImageRequest
	if err := json.Unmarshal([]byte(raw), &img); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if img.Extra == nil {
		t.Fatal("Extra map should be initialized (even if empty), not nil")
	}
	if len(img.Extra) != 0 {
		t.Fatalf("Extra = %+v, want empty", img.Extra)
	}
}

func TestImageRequest_UnmarshalJSON_MalformedJSON(t *testing.T) {
	// Called directly: a syntactically invalid top-level document never
	// reaches a custom UnmarshalJSON via json.Unmarshal (the stdlib scanner
	// validates syntax first), so we exercise the internal rawMap decode
	// error path explicitly.
	var img ImageRequest
	if err := img.UnmarshalJSON([]byte(`{"model":`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestImageRequest_UnmarshalJSON_KnownFieldTypeMismatch(t *testing.T) {
	// rawMap-stage unmarshal succeeds (it's valid generic JSON), but the
	// second, strongly-typed unmarshal into the known Alias struct fails
	// because "n" is declared as uint and a string is supplied.
	var img ImageRequest
	err := json.Unmarshal([]byte(`{"model":"dall-e-2","n":"not-a-number"}`), &img)
	if err == nil {
		t.Fatal("expected type-mismatch error from strongly-typed unmarshal")
	}
}

func TestImageRequest_MarshalJSON_DropsExtraFields(t *testing.T) {
	// FINDING (documented, not a bug — verifying intentional documented behavior):
	// internal/pkg/dto/openai_image.go MarshalJSON() explicitly does NOT merge
	// r.Extra back into the output (the merge loop is commented out with a
	// warning comment). This test locks in that any vendor-specific fields
	// captured into Extra during Unmarshal are silently dropped when the
	// struct is re-marshaled (e.g. before forwarding to a different vendor).
	raw := `{"model":"dall-e-3","prompt":"a cat","n":1,"vendor_specific":"should-be-dropped"}`
	var img ImageRequest
	if err := json.Unmarshal([]byte(raw), &img); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(img.Extra) != 1 {
		t.Fatalf("expected 1 extra field captured, got %+v", img.Extra)
	}

	out, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var outMap map[string]any
	if err := json.Unmarshal(out, &outMap); err != nil {
		t.Fatalf("unmarshal output error: %v", err)
	}
	if _, ok := outMap["vendor_specific"]; ok {
		t.Fatal("vendor_specific field must not appear in marshaled output")
	}
	if outMap["model"] != "dall-e-3" || outMap["prompt"] != "a cat" {
		t.Fatalf("known fields missing from output: %+v", outMap)
	}
}

func TestImageRequest_MarshalJSON_PropagatesInnerMarshalError(t *testing.T) {
	// A json.RawMessage field holding syntactically invalid JSON bytes causes
	// the underlying alias-struct Marshal to fail (encoding/json validates
	// RawMessage content via json.Compact before embedding it), exercising
	// the `if err != nil { return nil, err }` branch after common.Marshal(alias).
	img := ImageRequest{Model: "dall-e-2", Style: json.RawMessage("not valid json")}
	if _, err := img.MarshalJSON(); err == nil {
		t.Fatal("expected error from invalid embedded RawMessage field")
	}
}

func TestImageRequest_MarshalUnmarshalRoundTrip(t *testing.T) {
	img := ImageRequest{Model: "dall-e-2", Prompt: "roundtrip", N: 4, Size: "512x512"}
	data, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var back ImageRequest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if back.Model != img.Model || back.Prompt != img.Prompt || back.N != img.N || back.Size != img.Size {
		t.Fatalf("round trip mismatch: got %+v, want %+v", back, img)
	}
}

func TestGetJSONFieldNames(t *testing.T) {
	fields := GetJSONFieldNames(reflect.TypeOf(ImageRequest{}))
	for _, want := range []string{"model", "prompt", "n", "size", "quality"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("expected field %q present in known fields", want)
		}
	}
	// "-" tagged field (Extra) must be excluded.
	if _, ok := fields["Extra"]; ok {
		t.Error("field tagged json:\"-\" must be excluded")
	}
}

func TestGetJSONFieldNames_SkipsAnonymousFields(t *testing.T) {
	type Embedded struct {
		X string `json:"x"`
	}
	type WithAnon struct {
		Embedded
		Name string `json:"name"`
	}
	fields := GetJSONFieldNames(reflect.TypeOf(WithAnon{}))
	if _, ok := fields["name"]; !ok {
		t.Fatal("expected regular field 'name' present")
	}
	// The anonymous embedded field itself must be skipped (its own field name
	// "Embedded" must not appear as a known field key).
	if _, ok := fields["Embedded"]; ok {
		t.Fatal("anonymous embedded field must be skipped, not registered under its type name")
	}
	if len(fields) != 1 {
		t.Fatalf("fields = %+v, want exactly 1 (only 'name')", fields)
	}
}

func TestIndexComma(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"model,omitempty", 5},
		{"model", -1},
		{"", -1},
		{",", 0},
	}
	for _, c := range cases {
		if got := indexComma(c.in); got != c.want {
			t.Errorf("indexComma(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestImageRequest_GetTokenCountMeta(t *testing.T) {
	cases := []struct {
		name    string
		req     ImageRequest
		wantRat float64
	}{
		{"non-dalle model uses ratio 1*N", ImageRequest{Model: "some-other-model", N: 3}, 3},
		{"dall-e 256x256", ImageRequest{Model: "dall-e-2", Size: "256x256", N: 1}, 0.4},
		{"dall-e 512x512", ImageRequest{Model: "dall-e-2", Size: "512x512", N: 1}, 0.45},
		{"dall-e 1024x1024", ImageRequest{Model: "dall-e-3", Size: "1024x1024", N: 1}, 1},
		{"dall-e 1024x1792", ImageRequest{Model: "dall-e-3", Size: "1024x1792", N: 1}, 2},
		{"dall-e-3 hd quality doubles ratio", ImageRequest{Model: "dall-e-3", Size: "1024x1024", Quality: "hd", N: 1}, 2},
		{"dall-e-3 hd + wide size uses 1.5x not 2x", ImageRequest{Model: "dall-e-3", Size: "1024x1792", Quality: "hd", N: 1}, 3}, // 2 (size) * 1.5 (hd wide override)
		{"unrecognized size defaults ratio to 1", ImageRequest{Model: "dall-e-2", Size: "weird", N: 2}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			meta := c.req.GetTokenCountMeta()
			if meta.ImagePriceRatio != c.wantRat {
				t.Errorf("ImagePriceRatio = %v, want %v", meta.ImagePriceRatio, c.wantRat)
			}
			if meta.CombineText != c.req.Prompt {
				t.Errorf("CombineText = %q, want prompt %q", meta.CombineText, c.req.Prompt)
			}
			if meta.MaxTokens != 1584 {
				t.Errorf("MaxTokens = %d, want fixed 1584", meta.MaxTokens)
			}
		})
	}
}

func TestImageRequest_IsStreamAndSetModelName(t *testing.T) {
	c := newTestGinContext(t, "/")
	img := &ImageRequest{}
	if img.IsStream(c) {
		t.Fatal("image requests never stream")
	}
	img.Model = "old"
	img.SetModelName("")
	if img.Model != "old" {
		t.Fatalf("empty modelName must not overwrite")
	}
	img.SetModelName("dall-e-3")
	if img.Model != "dall-e-3" {
		t.Fatalf("Model = %q, want dall-e-3", img.Model)
	}
}
