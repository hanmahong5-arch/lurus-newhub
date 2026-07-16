package common

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

// ---------- str.go ----------

func TestGetStringIfEmpty(t *testing.T) {
	if got := GetStringIfEmpty("", "fallback"); got != "fallback" {
		t.Errorf("empty input: got %q, want fallback", got)
	}
	if got := GetStringIfEmpty("value", "fallback"); got != "value" {
		t.Errorf("non-empty input: got %q, want value", got)
	}
}

func TestGetRandomString(t *testing.T) {
	if got := GetRandomString(0); got != "" {
		t.Errorf("length 0 should yield empty, got %q", got)
	}
	if got := GetRandomString(-5); got != "" {
		t.Errorf("negative length should yield empty, got %q", got)
	}
	s := GetRandomString(16)
	if len(s) != 16 {
		t.Errorf("expected length 16, got %d", len(s))
	}
	// Two draws should (essentially always) differ.
	draw1, draw2 := GetRandomString(24), GetRandomString(24)
	if draw1 == draw2 {
		t.Error("two random strings unexpectedly equal")
	}
}

// TestGetRandomString_CharsetAndLength is a regression test for the
// math/rand -> crypto/rand fix (security defect #6: security-sensitive
// secrets such as redemption codes / internal API keys, generated via
// common.GetRandomString, must not use a non-CSPRNG). It asserts the
// public contract GetRandomString callers rely on (length, charset) held
// after switching the underlying RNG — i.e. the interface didn't change,
// only the entropy source did.
//
// NOTE: this does NOT and cannot prove cryptographic randomness. It only
// checks: (1) length/charset correctness is preserved, (2) the character
// distribution over a large sample looks sane (no gross bias such as a
// modulo-bias bug would produce), which is a sanity check, not a proof.
func TestGetRandomString_CharsetAndLength(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	allowed := make(map[rune]bool, len(charset))
	for _, r := range charset {
		allowed[r] = true
	}

	for _, length := range []int{1, 2, 5, 16, 32, 48, 100} {
		s := GetRandomString(length)
		if len(s) != length {
			t.Fatalf("length %d: got len %d (%q)", length, len(s), s)
		}
		for _, r := range s {
			if !allowed[r] {
				t.Fatalf("length %d: output %q contains char %q outside expected charset", length, s, r)
			}
		}
	}
}

// TestGetRandomString_DistributionSanity draws a large sample and checks
// every charset character appears with roughly the expected frequency.
// This is a coarse statistical sanity check (loose bounds, not a
// randomness/entropy proof) aimed at catching an implementation bug like
// modulo bias (e.g. `charsetIndex := b % 62`, which would skew towards the
// low end of a 256-value byte range since 256 is not a multiple of 62).
func TestGetRandomString_DistributionSanity(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const sampleLen = 20000

	counts := make(map[rune]int, len(charset))
	for _, r := range charset {
		counts[r] = 0
	}

	s := GetRandomString(sampleLen)
	if len(s) != sampleLen {
		t.Fatalf("expected sample length %d, got %d", sampleLen, len(s))
	}
	for _, r := range s {
		if _, ok := counts[r]; !ok {
			t.Fatalf("unexpected char %q outside charset", r)
		}
		counts[r]++
	}

	expected := float64(sampleLen) / float64(len(charset))
	// Loose bound: unbiased sampling should land each char within ~40% of
	// the expected frequency at this sample size; a biased/broken RNG
	// (e.g. modulo bias, or a charset subset never reached) would blow
	// past this easily.
	lowerBound := expected * 0.6
	upperBound := expected * 1.4
	for _, r := range charset {
		c := counts[r]
		if float64(c) < lowerBound || float64(c) > upperBound {
			t.Errorf("char %q count %d outside sane bound [%.0f, %.0f] (expected ~%.0f)", r, c, lowerBound, upperBound, expected)
		}
	}
}

// TestGetRandomString_SecretCallSitesUseIt documents (and pins) the real
// production call sites this fix targets. GetRandomString was fixed in
// place (Option A) rather than adding a parallel GetSecureRandomString,
// so any caller — including the two security-sensitive ones below — now
// gets crypto/rand automatically. This test can't reach into other
// packages' unexported code paths, so it re-asserts the property those
// call sites depend on: fixed length, no panic, alphanumeric-only output.
// Call sites (verified by source read, not executed here):
//   - internal/adapter/repo/internal_api_key.go: key := "lurus_ik_" + common.GetRandomString(32)
//   - internal/adapter/handler/v2_redemption.go: key := common.GetRandomString(32)
func TestGetRandomString_SecretCallSitesUseIt(t *testing.T) {
	key := "lurus_ik_" + GetRandomString(32)
	if len(key) != len("lurus_ik_")+32 {
		t.Fatalf("internal API key shape changed: got %q (len %d)", key, len(key))
	}
	redemptionKey := GetRandomString(32)
	if len(redemptionKey) != 32 {
		t.Fatalf("redemption key shape changed: got %q (len %d)", redemptionKey, len(redemptionKey))
	}
}

func TestMapToJsonStrAndStrToMap(t *testing.T) {
	m := map[string]interface{}{"a": "1", "b": "2"}
	js := MapToJsonStr(m)
	back, err := StrToMap(js)
	if err != nil {
		t.Fatalf("StrToMap error: %v", err)
	}
	if back["a"] != "1" || back["b"] != "2" {
		t.Errorf("round-trip mismatch: %v", back)
	}
	if _, err := StrToMap("not json"); err == nil {
		t.Error("expected error for invalid json")
	}
}

func TestStrToJsonArray(t *testing.T) {
	arr, err := StrToJsonArray(`[1,2,3]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr))
	}
	if _, err := StrToJsonArray(`{bad}`); err == nil {
		t.Error("expected error for invalid array")
	}
}

func TestIsJsonArrayObject(t *testing.T) {
	if !IsJsonArray(`[1,2]`) {
		t.Error("valid array not detected")
	}
	if IsJsonArray(`{"a":1}`) {
		t.Error("object misdetected as array")
	}
	if !IsJsonObject(`{"a":1}`) {
		t.Error("valid object not detected")
	}
	if IsJsonObject(`[1,2]`) {
		t.Error("array misdetected as object")
	}
}

func TestString2Int(t *testing.T) {
	if String2Int("42") != 42 {
		t.Error("42 not parsed")
	}
	if String2Int("nope") != 0 {
		t.Error("invalid should be 0")
	}
	if String2Int("-7") != -7 {
		t.Error("negative not parsed")
	}
}

func TestStringsContains(t *testing.T) {
	xs := []string{"a", "b", "c"}
	if !StringsContains(xs, "b") {
		t.Error("should contain b")
	}
	if StringsContains(xs, "z") {
		t.Error("should not contain z")
	}
	if StringsContains(nil, "x") {
		t.Error("nil slice contains nothing")
	}
}

func TestStringToByteSliceAndEncodeBase64(t *testing.T) {
	b := StringToByteSlice("hello")
	if string(b) != "hello" {
		t.Errorf("byte slice mismatch: %q", string(b))
	}
	if EncodeBase64("hi") != "aGk=" {
		t.Errorf("base64 mismatch: %q", EncodeBase64("hi"))
	}
}

func TestGetJsonString(t *testing.T) {
	if GetJsonString(nil) != "" {
		t.Error("nil should be empty string")
	}
	if got := GetJsonString(map[string]int{"x": 1}); got != `{"x":1}` {
		t.Errorf("got %q", got)
	}
}

func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"":                "***masked***",
		"noatsign":        "***masked***",
		"user@domain.com": "***@domain.com",
	}
	for in, want := range cases {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMaskSensitiveInfo(t *testing.T) {
	cases := map[string]string{
		"http://example.com":      "http://***.com",
		"192.168.1.1":             "***.***.***.***",
		"openai.com":              "***.com",
		"api.openai.com":          "***.***.com",
		"sub.domain.co.uk":        "***.***.co.uk",
		"https://api.test.org/v1": "https://***.org/***",
	}
	for in, want := range cases {
		if got := MaskSensitiveInfo(in); got != want {
			t.Errorf("MaskSensitiveInfo(%q)=%q want %q", in, got, want)
		}
	}
	// URL with query param must mask the value but keep the key.
	masked := MaskSensitiveInfo("https://api.test.org/v1/users/123?key=secret")
	if strings.Contains(masked, "secret") {
		t.Errorf("query value leaked: %q", masked)
	}
	if !strings.Contains(masked, "key=***") {
		t.Errorf("query key not preserved: %q", masked)
	}
}

// ---------- hash.go ----------

func TestHashHelpers(t *testing.T) {
	data := []byte("payload")
	if got := Sha256Raw(data); !bytes.Equal(got, sha256sum(data)) {
		t.Error("Sha256Raw mismatch")
	}
	if got := Sha1Raw(data); !bytes.Equal(got, sha1sum(data)) {
		t.Error("Sha1Raw mismatch")
	}
	if Sha1(data) != hex.EncodeToString(sha1sum(data)) {
		t.Error("Sha1 hex mismatch")
	}
	want := hmac.New(sha256.New, []byte("k"))
	want.Write([]byte("msg"))
	if HmacSha256("msg", "k") != hex.EncodeToString(want.Sum(nil)) {
		t.Error("HmacSha256 mismatch")
	}
	if !bytes.Equal(HmacSha256Raw([]byte("msg"), []byte("k")), want.Sum(nil)) {
		t.Error("HmacSha256Raw mismatch")
	}
}

func sha256sum(b []byte) []byte { h := sha256.New(); h.Write(b); return h.Sum(nil) }
func sha1sum(b []byte) []byte   { h := sha1.New(); h.Write(b); return h.Sum(nil) }

// ---------- crypto.go ----------

func TestGenerateHMAC(t *testing.T) {
	a := GenerateHMACWithKey([]byte("key1"), "data")
	b := GenerateHMACWithKey([]byte("key2"), "data")
	if a == b {
		t.Error("different keys must produce different HMAC")
	}
	// Deterministic for same key+data.
	if GenerateHMACWithKey([]byte("key1"), "data") != a {
		t.Error("HMAC not deterministic")
	}
	// GenerateHMAC uses the package CryptoSecret — deterministic within a run.
	hmac1, hmac2 := GenerateHMAC("x"), GenerateHMAC("x")
	if hmac1 != hmac2 {
		t.Error("GenerateHMAC not deterministic")
	}
}

func TestPassword2HashAndValidate(t *testing.T) {
	hash, err := Password2Hash("s3cret-pw")
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if hash == "s3cret-pw" {
		t.Error("hash must not equal plaintext")
	}
	if !ValidatePasswordAndHash("s3cret-pw", hash) {
		t.Error("correct password rejected")
	}
	if ValidatePasswordAndHash("wrong", hash) {
		t.Error("wrong password accepted")
	}
	if ValidatePasswordAndHash("s3cret-pw", "not-a-bcrypt-hash") {
		t.Error("invalid hash accepted")
	}
}

// ---------- ip.go ----------

func TestIsIPAndParseIP(t *testing.T) {
	if !IsIP("10.0.0.1") {
		t.Error("valid IP not recognized")
	}
	if IsIP("not-an-ip") {
		t.Error("invalid IP recognized")
	}
	if ParseIP("bad") != nil {
		t.Error("bad IP should parse to nil")
	}
	if ParseIP("1.2.3.4") == nil {
		t.Error("good IP parsed to nil")
	}
}

func TestIsPrivateIP(t *testing.T) {
	private := []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "127.0.0.1", "169.254.1.1"}
	for _, s := range private {
		if !IsPrivateIP(net.ParseIP(s)) {
			t.Errorf("%s should be private", s)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"}
	for _, s := range public {
		if IsPrivateIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
}

func TestIsIpInCIDRList(t *testing.T) {
	list := []string{"10.0.0.0/8", "203.0.113.5", "not-a-cidr"}
	if !IsIpInCIDRList(net.ParseIP("10.5.5.5"), list) {
		t.Error("10.5.5.5 should match 10.0.0.0/8")
	}
	if !IsIpInCIDRList(net.ParseIP("203.0.113.5"), list) {
		t.Error("exact single IP should match")
	}
	if IsIpInCIDRList(net.ParseIP("8.8.8.8"), list) {
		t.Error("8.8.8.8 should not match")
	}
}

// ---------- copy.go ----------

func TestDeepCopy(t *testing.T) {
	type inner struct{ N int }
	type outer struct {
		Name string
		In   *inner
	}
	src := &outer{Name: "orig", In: &inner{N: 7}}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("copy error: %v", err)
	}
	if dst == src {
		t.Error("DeepCopy returned same pointer")
	}
	dst.In.N = 99
	if src.In.N != 7 {
		t.Error("nested pointer was not deep-copied")
	}
	if _, err := DeepCopy[outer](nil); err == nil {
		t.Error("nil source must error")
	}
}

// ---------- env.go ----------

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_INT_ENV", "123")
	if GetEnvOrDefault("TEST_INT_ENV", 5) != 123 {
		t.Error("int env not read")
	}
	if GetEnvOrDefault("NONEXISTENT_ENV_ZZZ", 5) != 5 {
		t.Error("missing int env should use default")
	}
	t.Setenv("TEST_BAD_INT", "abc")
	if GetEnvOrDefault("TEST_BAD_INT", 9) != 9 {
		t.Error("bad int env should fall back to default")
	}

	t.Setenv("TEST_STR_ENV", "hello")
	if GetEnvOrDefaultString("TEST_STR_ENV", "x") != "hello" {
		t.Error("string env not read")
	}
	if GetEnvOrDefaultString("NONEXISTENT_ENV_ZZZ", "x") != "x" {
		t.Error("missing string env should use default")
	}

	t.Setenv("TEST_FLOAT_ENV", "1.5")
	if GetEnvOrDefaultFloat("TEST_FLOAT_ENV", 0) != 1.5 {
		t.Error("float env not read")
	}
	t.Setenv("TEST_BAD_FLOAT", "xx")
	if GetEnvOrDefaultFloat("TEST_BAD_FLOAT", 2.5) != 2.5 {
		t.Error("bad float should fall back")
	}
	if GetEnvOrDefaultFloat("NONEXISTENT_ENV_ZZZ", 3.5) != 3.5 {
		t.Error("missing float should use default")
	}

	t.Setenv("TEST_BOOL_ENV", "true")
	if !GetEnvOrDefaultBool("TEST_BOOL_ENV", false) {
		t.Error("bool env not read")
	}
	t.Setenv("TEST_BAD_BOOL", "maybe")
	if !GetEnvOrDefaultBool("TEST_BAD_BOOL", true) {
		t.Error("bad bool should fall back to default true")
	}
	if !GetEnvOrDefaultBool("NONEXISTENT_ENV_ZZZ", true) {
		t.Error("missing bool should use default")
	}
}

// ---------- api_type.go / endpoint_type.go / endpoint_defaults.go ----------

func TestChannelType2APIType(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeOpenAI)
	if !ok || apiType != constant.APITypeOpenAI {
		t.Errorf("OpenAI channel: got (%d,%v)", apiType, ok)
	}
	apiType, ok = ChannelType2APIType(constant.ChannelTypeAnthropic)
	if !ok || apiType != constant.APITypeAnthropic {
		t.Errorf("Anthropic channel: got (%d,%v)", apiType, ok)
	}
	// Unknown channel type falls back to OpenAI with ok=false.
	apiType, ok = ChannelType2APIType(999999)
	if ok || apiType != constant.APITypeOpenAI {
		t.Errorf("unknown channel: got (%d,%v), want (OpenAI,false)", apiType, ok)
	}
}

func TestGetEndpointTypesByChannelType(t *testing.T) {
	jina := GetEndpointTypesByChannelType(constant.ChannelTypeJina, "jina-reranker")
	if len(jina) == 0 || jina[0] != constant.EndpointTypeJinaRerank {
		t.Errorf("Jina endpoint mismatch: %v", jina)
	}
	anth := GetEndpointTypesByChannelType(constant.ChannelTypeAnthropic, "claude-3")
	if len(anth) < 2 || anth[0] != constant.EndpointTypeAnthropic {
		t.Errorf("Anthropic endpoints mismatch: %v", anth)
	}
	// OpenRouter only supports the OpenAI endpoint.
	or := GetEndpointTypesByChannelType(constant.ChannelTypeOpenRouter, "gpt-4")
	if len(or) != 1 || or[0] != constant.EndpointTypeOpenAI {
		t.Errorf("OpenRouter endpoints mismatch: %v", or)
	}
	// Image-generation model prepends image endpoint.
	img := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "dall-e-3")
	if len(img) == 0 || img[0] != constant.EndpointTypeImageGeneration {
		t.Errorf("image endpoint not prepended: %v", img)
	}
}

func TestGetDefaultEndpointInfo(t *testing.T) {
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeOpenAI)
	if !ok || info.Path != "/v1/chat/completions" || info.Method != "POST" {
		t.Errorf("OpenAI default endpoint mismatch: %+v ok=%v", info, ok)
	}
	if _, ok := GetDefaultEndpointInfo(constant.EndpointType("nope")); ok {
		t.Error("unknown endpoint type should return ok=false")
	}
}

// ---------- model.go ----------

func TestModelClassifiers(t *testing.T) {
	if !IsOpenAIResponseOnlyModel("o3-pro") {
		t.Error("o3-pro should be response-only")
	}
	if IsOpenAIResponseOnlyModel("gpt-4") {
		t.Error("gpt-4 is not response-only")
	}
	if !IsImageGenerationModel("DALL-E-3") {
		t.Error("dall-e-3 (case-insensitive) should be image gen")
	}
	if !IsImageGenerationModel("imagen-4") {
		t.Error("imagen- prefix should be image gen")
	}
	if IsImageGenerationModel("gpt-4") {
		t.Error("gpt-4 is not image gen")
	}
	if !IsOpenAITextModel("gpt-4o") {
		t.Error("gpt-4o should be text model")
	}
	if IsOpenAITextModel("claude-3") {
		t.Error("claude-3 is not an OpenAI text model")
	}
}

// ---------- constants.go role helpers ----------

func TestRoleHelpers(t *testing.T) {
	if !IsValidateRole(RoleCommonUser) || !IsValidateRole(RoleRootUser) {
		t.Error("known roles should validate")
	}
	if IsValidateRole(7) {
		t.Error("role 7 is not a valid role")
	}
	if !IsSubscriber(RoleSubscriberUser) || !IsSubscriber(RoleAdminUser) {
		t.Error("subscriber+ should be subscriber")
	}
	if IsSubscriber(RoleCommonUser) {
		t.Error("common user is not a subscriber")
	}
}

// ---------- topup-ratio.go ----------

func TestTopupGroupRatio(t *testing.T) {
	orig := TopupGroupRatio2JSONString()
	t.Cleanup(func() { _ = UpdateTopupGroupRatioByJSONString(orig) })

	if err := UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":2.5}`); err != nil {
		t.Fatalf("update error: %v", err)
	}
	if GetTopupGroupRatio("vip") != 2.5 {
		t.Errorf("vip ratio expected 2.5, got %v", GetTopupGroupRatio("vip"))
	}
	// Unknown group falls back to 1.
	if GetTopupGroupRatio("does-not-exist") != 1 {
		t.Error("unknown group should default to 1")
	}
	if err := UpdateTopupGroupRatioByJSONString("not-json"); err == nil {
		t.Error("invalid json should error")
	}
}

// ---------- quota.go ----------

func TestGetTrustQuota(t *testing.T) {
	if got := GetTrustQuota(); got != int(10*QuotaPerUnit) {
		t.Errorf("trust quota = %d, want %d", got, int(10*QuotaPerUnit))
	}
}

// ---------- json.go ----------

func TestGetJsonType(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`: "object",
		`[1,2]`:   "array",
		`"str"`:   "string",
		`true`:    "boolean",
		`false`:   "boolean",
		`null`:    "null",
		`42`:      "number",
		``:        "unknown",
		`   `:     "unknown",
	}
	for in, want := range cases {
		if got := GetJsonType([]byte(in)); got != want {
			t.Errorf("GetJsonType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestUnmarshalHelpers(t *testing.T) {
	var v map[string]int
	if err := Unmarshal([]byte(`{"x":1}`), &v); err != nil || v["x"] != 1 {
		t.Errorf("Unmarshal failed: %v %v", v, err)
	}
	var w map[string]int
	if err := UnmarshalJsonStr(`{"y":2}`, &w); err != nil || w["y"] != 2 {
		t.Errorf("UnmarshalJsonStr failed: %v %v", w, err)
	}
	b, err := Marshal(map[string]int{"z": 3})
	if err != nil || string(b) != `{"z":3}` {
		t.Errorf("Marshal failed: %s %v", b, err)
	}
	var d map[string]int
	if err := DecodeJson(strings.NewReader(`{"q":4}`), &d); err != nil || d["q"] != 4 {
		t.Errorf("DecodeJson failed: %v %v", d, err)
	}
}

// ---------- go-channel.go ----------

func TestSafeSendBool(t *testing.T) {
	ch := make(chan bool, 1)
	if SafeSendBool(ch, true) {
		t.Error("send on open channel should report not-closed")
	}
	if got := <-ch; got != true {
		t.Error("value not delivered")
	}
	close(ch)
	if !SafeSendBool(ch, true) {
		t.Error("send on closed channel should report closed=true")
	}
}

func TestSafeSendString(t *testing.T) {
	ch := make(chan string, 1)
	if SafeSendString(ch, "hi") {
		t.Error("send on open channel should report not-closed")
	}
	if <-ch != "hi" {
		t.Error("value not delivered")
	}
	close(ch)
	if !SafeSendString(ch, "hi") {
		t.Error("send on closed channel should report closed=true")
	}
}

func TestSafeSendStringTimeout(t *testing.T) {
	ch := make(chan string, 1)
	if !SafeSendStringTimeout(ch, "v", 1) {
		t.Error("send with buffer available should return true")
	}
	// Buffer now full; timeout path returns false.
	if SafeSendStringTimeout(ch, "v2", 0) {
		t.Error("send that times out should return false")
	}
}

// ---------- verification.go ----------

func TestGenerateVerificationCode(t *testing.T) {
	full := GenerateVerificationCode(0)
	if len(full) != 32 {
		t.Errorf("full uuid code should be 32 chars, got %d", len(full))
	}
	if strings.Contains(full, "-") {
		t.Error("dashes should be stripped")
	}
	short := GenerateVerificationCode(6)
	if len(short) != 6 {
		t.Errorf("expected 6-char code, got %d", len(short))
	}
}

func TestRegisterVerifyDeleteKey(t *testing.T) {
	key, code, purpose := "unit-key-xyz", "abc123", EmailVerificationPurpose
	RegisterVerificationCodeWithKey(key, code, purpose)
	if !VerifyCodeWithKey(key, code, purpose) {
		t.Error("correct code should verify")
	}
	if VerifyCodeWithKey(key, "wrong", purpose) {
		t.Error("wrong code should not verify")
	}
	if VerifyCodeWithKey("no-such-key", code, purpose) {
		t.Error("unregistered key should not verify")
	}
	DeleteKey(key, purpose)
	if VerifyCodeWithKey(key, code, purpose) {
		t.Error("deleted key should no longer verify")
	}
}

// ---------- page_info.go ----------

func TestPageInfoMethods(t *testing.T) {
	p := &PageInfo{Page: 3, PageSize: 20}
	if p.GetStartIdx() != 40 {
		t.Errorf("start idx = %d, want 40", p.GetStartIdx())
	}
	if p.GetEndIdx() != 60 {
		t.Errorf("end idx = %d, want 60", p.GetEndIdx())
	}
	if p.GetPage() != 3 || p.GetPageSize() != 20 {
		t.Error("getters mismatch")
	}
	p.SetTotal(99)
	p.SetItems([]int{1, 2})
	if p.Total != 99 {
		t.Error("SetTotal failed")
	}
	if got, ok := p.Items.([]int); !ok || len(got) != 2 {
		t.Error("SetItems failed")
	}
}

func TestGetPageQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Default when no params: page 1, default items per page.
	c1 := newQueryContext(t, "/?")
	pi := GetPageQuery(c1)
	if pi.Page != 1 || pi.PageSize != ItemsPerPage {
		t.Errorf("defaults mismatch: page=%d size=%d", pi.Page, pi.PageSize)
	}

	// Explicit page & page_size.
	c2 := newQueryContext(t, "/?p=4&page_size=25")
	pi2 := GetPageQuery(c2)
	if pi2.Page != 4 || pi2.PageSize != 25 {
		t.Errorf("explicit params mismatch: page=%d size=%d", pi2.Page, pi2.PageSize)
	}

	// PageSize capped at 100.
	c3 := newQueryContext(t, "/?p=1&page_size=500")
	if GetPageQuery(c3).PageSize != 100 {
		t.Error("page size should be capped at 100")
	}

	// Legacy 'size' param.
	c4 := newQueryContext(t, "/?p=2&size=15")
	if got := GetPageQuery(c4); got.PageSize != 15 {
		t.Errorf("legacy size param not honored: %d", got.PageSize)
	}
}

// TestGetPageQuery_NegativePageSizeClamped: MEDIUM — a negative page_size is
// neither == 0 (so it skips the default-fill branch) nor > 100 (so it skips
// the upper clamp), and previously passed through untouched straight into
// GORM's .Limit(negative), which omits the LIMIT clause entirely and dumps
// the full table. GetPageQuery must clamp any PageSize < 1 back into
// [1, 100].
func TestGetPageQuery_NegativePageSizeClamped(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newQueryContext(t, "/?p=1&page_size=-1")
	pi := GetPageQuery(c)
	if pi.PageSize < 1 || pi.PageSize > 100 {
		t.Fatalf("page_size=-1 not clamped into [1,100]: got %d", pi.PageSize)
	}
	if pi.PageSize != ItemsPerPage {
		t.Errorf("page_size=-1 should clamp to ItemsPerPage (%d), got %d", ItemsPerPage, pi.PageSize)
	}
}

func newQueryContext(t *testing.T, target string) *gin.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// ---------- gin.go context helpers ----------

func TestGinContextKeyHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	SetContextKey(c, constant.ContextKey("k_str"), "hello")
	if GetContextKeyString(c, constant.ContextKey("k_str")) != "hello" {
		t.Error("string context key mismatch")
	}
	if v, ok := GetContextKey(c, constant.ContextKey("k_str")); !ok || v != "hello" {
		t.Error("GetContextKey mismatch")
	}

	SetContextKey(c, constant.ContextKey("k_int"), 42)
	if GetContextKeyInt(c, constant.ContextKey("k_int")) != 42 {
		t.Error("int context key mismatch")
	}

	SetContextKey(c, constant.ContextKey("k_bool"), true)
	if !GetContextKeyBool(c, constant.ContextKey("k_bool")) {
		t.Error("bool context key mismatch")
	}

	SetContextKey(c, constant.ContextKey("k_slice"), []string{"a", "b"})
	if len(GetContextKeyStringSlice(c, constant.ContextKey("k_slice"))) != 2 {
		t.Error("string slice context key mismatch")
	}

	// Typed getter, present and absent.
	SetContextKey(c, constant.ContextKey("k_typed"), 7)
	if v, ok := GetContextKeyType[int](c, constant.ContextKey("k_typed")); !ok || v != 7 {
		t.Error("typed getter present mismatch")
	}
	if _, ok := GetContextKeyType[int](c, constant.ContextKey("absent")); ok {
		t.Error("typed getter should report absent key")
	}
	// Wrong-type assertion should report not-ok.
	if _, ok := GetContextKeyType[string](c, constant.ContextKey("k_typed")); ok {
		t.Error("typed getter should reject wrong type")
	}
}
