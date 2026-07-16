package totp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	pqtotp "github.com/pquerna/otp/totp"
)

func withMemoryBackend(t *testing.T) {
	t.Helper()
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	ResetStateForTest()
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		ResetStateForTest()
	})
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	const plain = "JBSWY3DPEHPK3PXP"
	enc, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, "v1:") {
		t.Fatalf("ciphertext missing version prefix: %q", enc)
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("ciphertext leaks plaintext: %q", enc)
	}
	dec, err := DecryptSecret(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec, plain)
	}

	// Two encryptions of the same plaintext must differ (random nonce).
	enc2, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt2: %v", err)
	}
	if enc == enc2 {
		t.Fatal("nonce reuse: two encryptions produced identical ciphertext")
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "v1:!!!notbase64!!!", "v1:aGVsbG8="} {
		if _, err := DecryptSecret(bad); err == nil {
			t.Fatalf("decrypt(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestGenerateEnrollmentAndValidate(t *testing.T) {
	secret, url, err := GenerateEnrollment("alice")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if secret == "" || !strings.HasPrefix(url, "otpauth://totp/") {
		t.Fatalf("unexpected enrollment: secret=%q url=%q", secret, url)
	}
	if !strings.Contains(url, "secret="+secret) {
		t.Fatalf("url does not carry the secret: %q", url)
	}

	code, err := pqtotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if !ValidateCode(secret, code) {
		t.Fatal("freshly generated code did not validate")
	}
	if ValidateCode(secret, "000000") && code != "000000" {
		t.Fatal("obviously wrong code validated")
	}
	if ValidateCode(secret, "") {
		t.Fatal("empty code validated")
	}
}

func TestMarkCodeUsedBlocksReplay(t *testing.T) {
	withMemoryBackend(t)
	ctx := context.Background()

	if !MarkCodeUsed(ctx, 1, "123456") {
		t.Fatal("first use should be accepted")
	}
	if MarkCodeUsed(ctx, 1, "123456") {
		t.Fatal("replay of same (user, code) should be rejected")
	}
	// Different user, same code → independent budget.
	if !MarkCodeUsed(ctx, 2, "123456") {
		t.Fatal("same code for a different user should be accepted")
	}
	// Expired entries are swept and usable again.
	prevNow := timeNowFn
	timeNowFn = func() time.Time { return time.Now().Add(replayTTL + time.Second) }
	defer func() { timeNowFn = prevNow }()
	if !MarkCodeUsed(ctx, 1, "123456") {
		t.Fatal("after the replay TTL the code slot should be free again")
	}
}

func TestFailureThrottle(t *testing.T) {
	withMemoryBackend(t)
	ctx := context.Background()

	for i := 0; i < failLimit; i++ {
		if !AllowAttempt(ctx, 7) {
			t.Fatalf("attempt %d should still be allowed", i+1)
		}
		RecordFailure(ctx, 7)
	}
	if AllowAttempt(ctx, 7) {
		t.Fatalf("attempt after %d failures should be blocked", failLimit)
	}
	// Other users are unaffected.
	if !AllowAttempt(ctx, 8) {
		t.Fatal("unrelated user throttled")
	}
	ClearFailures(ctx, 7)
	if !AllowAttempt(ctx, 7) {
		t.Fatal("ClearFailures should reset the budget")
	}
}
