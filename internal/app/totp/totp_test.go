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

func TestGenerateBackupCodes_CountFormatAndUniqueness(t *testing.T) {
	codes, err := GenerateBackupCodes(BackupCodeCount)
	if err != nil {
		t.Fatalf("GenerateBackupCodes: %v", err)
	}
	if len(codes) != BackupCodeCount {
		t.Fatalf("len(codes) = %d, want %d", len(codes), BackupCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("code %q not in XXXX-XXXX form", c)
		}
		for _, ch := range strings.ReplaceAll(c, "-", "") {
			if strings.ContainsRune("0O1I", ch) {
				t.Fatalf("code %q uses an excluded ambiguous character %q", c, ch)
			}
		}
		if seen[c] {
			t.Fatalf("duplicate code generated in one batch: %q", c)
		}
		seen[c] = true
	}
}

func TestHashBackupCode_DeterministicAndUserScoped(t *testing.T) {
	h1 := HashBackupCode(1, "ABCD-EFGH")
	h2 := HashBackupCode(1, "ABCD-EFGH")
	if h1 != h2 {
		t.Fatalf("HashBackupCode not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64 (hex sha256)", len(h1))
	}
	// Same code, different user -> different hash (domain separation, so
	// two users who are independently issued the same code string cannot
	// collide on the unique index).
	h3 := HashBackupCode(2, "ABCD-EFGH")
	if h1 == h3 {
		t.Fatal("HashBackupCode must be user-scoped: same code for two users hashed identically")
	}
	// Case/whitespace-insensitive normalization: a user pasting lowercase or
	// with stray whitespace must still match what was stored.
	h4 := HashBackupCode(1, "  abcd-efgh  ")
	if h1 != h4 {
		t.Fatal("HashBackupCode must normalize case/whitespace")
	}
	// A user retyping a code without the dash, or with a space in place of
	// it (both plausible when copying from a printed sheet), must still
	// match the canonical "XXXX-XXXX" hash — otherwise they burn one of
	// the 5 throttle attempts on a code that is actually valid.
	if h5 := HashBackupCode(1, "abcdefgh"); h5 != h1 {
		t.Fatal("HashBackupCode must match a dash-less retype of the same code")
	}
	if h6 := HashBackupCode(1, "abcd efgh"); h6 != h1 {
		t.Fatal("HashBackupCode must match a space-separated retype of the same code")
	}
	// Never equal to the plaintext-adjacent EncryptSecret scheme's output
	// shape (sanity: this is a hash, not a reversible ciphertext).
	if strings.HasPrefix(h1, encVersionPrefix) {
		t.Fatal("HashBackupCode output must not look like an EncryptSecret ciphertext")
	}
}
