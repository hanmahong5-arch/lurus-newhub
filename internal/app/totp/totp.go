// Package totp implements the TOTP (RFC 6238) factor for the
// secure-verification step-up flow: secret generation, AES-256-GCM secret
// encryption at rest, code validation, single-use (anti-replay) code marking
// and per-user failure throttling.
//
// Storage of the encrypted secret lives in internal/adapter/repo/user_totp.go;
// HTTP wiring lives in internal/adapter/handler/totp.go and
// internal/adapter/handler/secure_verification.go.
package totp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/pquerna/otp"
	pqtotp "github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
)

const (
	// encVersionPrefix versions the ciphertext layout so the scheme can be
	// rotated later without guessing.
	encVersionPrefix = "v1:"

	// replayTTL is how long a consumed code stays blocked. It must cover the
	// validation window (30s period ± 1 step skew accepted by Validate).
	replayTTL = 90 * time.Second

	// failWindow / failLimit throttle wrong-code attempts per user.
	failWindow = 5 * time.Minute
	failLimit  = 5
)

// ErrDecryptFailed is returned when the stored ciphertext cannot be opened
// (wrong CRYPTO_SECRET or corrupted value). Callers must treat it as
// "verification unavailable", never as "not enrolled".
var ErrDecryptFailed = errors.New("totp: secret decrypt failed")

func gcmFromSecret() (cipher.AEAD, error) {
	// Derive a fixed-length AES-256 key from the deployment secret. Reuses the
	// same secret source as GenerateHMAC (CRYPTO_SECRET, falling back to
	// SESSION_SECRET via common/init.go).
	key := sha256.Sum256([]byte(common.CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptSecret encrypts a TOTP shared secret for at-rest storage.
// Output: "v1:" + base64(nonce || ciphertext).
func EncryptSecret(plain string) (string, error) {
	gcm, err := gcmFromSecret()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encVersionPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptSecret reverses EncryptSecret.
func DecryptSecret(enc string) (string, error) {
	if !strings.HasPrefix(enc, encVersionPrefix) {
		return "", ErrDecryptFailed
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, encVersionPrefix))
	if err != nil {
		return "", ErrDecryptFailed
	}
	gcm, err := gcmFromSecret()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", ErrDecryptFailed
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plain), nil
}

// GenerateEnrollment creates a fresh TOTP secret for the given account and
// returns the base32 secret plus the otpauth:// provisioning URL.
func GenerateEnrollment(accountName string) (secret string, url string, err error) {
	if accountName == "" {
		accountName = "user"
	}
	key, err := pqtotp.Generate(pqtotp.GenerateOpts{
		Issuer:      common.SystemName,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1, // authenticator-app default
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateCode reports whether code is currently valid for secret.
func ValidateCode(secret, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	return pqtotp.Validate(code, secret)
}

// ---------------------------------------------------------------------------
// TOTP recovery (backup) codes. Minted on confirm/regenerate, each one is a
// single-use credential that lets a user who lost their authenticator app
// pass secure-verification without it. The server never needs the plaintext
// back (a code is presented once and consumed), so these are hashed
// one-way — never encrypted like the TOTP secret above.
// ---------------------------------------------------------------------------

// BackupCodeCount is how many recovery codes TotpConfirm and the regenerate
// endpoint mint at once.
const BackupCodeCount = 10

// backupCodeAlphabet excludes 0/O/1/I to avoid transcription ambiguity when
// a user copies a code down by hand.
const backupCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// GenerateBackupCodes returns n freshly generated recovery codes in
// "XXXX-XXXX" form (crypto/rand). Nothing here persists them — the caller
// hashes each with HashBackupCode before storing, and returns the plaintext
// to the client exactly once.
func GenerateBackupCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, err := generateOneBackupCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func generateOneBackupCode() (string, error) {
	b := make([]byte, 8)
	alphabetLen := big.NewInt(int64(len(backupCodeAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		b[i] = backupCodeAlphabet[idx.Int64()]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

// HashBackupCode computes the one-way digest stored for a backup code
// (hex SHA-256 of "<user_id>:<normalized code>"). The user id is mixed in
// as a domain separator so identical codes minted for two different users
// cannot collide on the unique index (entity.UserTOTPBackupCode.CodeHash).
// Normalization (see normalizeBackupCode) uppercases, strips the dash and
// any internal whitespace, and re-inserts the canonical dash — so a user
// who types "abcd efgh" or "ABCDEFGH" from a printed sheet, not just one
// who mistypes case or leaves surrounding whitespace, still matches the
// stored hash.
func HashBackupCode(userId int, code string) string {
	normalized := normalizeBackupCode(code)
	sum := sha256.Sum256([]byte(strconv.Itoa(userId) + ":" + normalized))
	return hex.EncodeToString(sum[:])
}

// normalizeBackupCode canonicalizes user-typed backup-code input before
// hashing: uppercase, strip the dash and any whitespace, then — only when
// exactly 8 alphabet characters remain — re-insert the dash at "XXXX-XXXX"
// position to match GenerateBackupCodes' output shape. Input that does not
// reduce to exactly 8 characters (too short, too long, or containing other
// punctuation) is returned uppercased/trimmed but otherwise unchanged: it
// will simply not match any stored hash, which is the correct fail-closed
// outcome rather than a specially-handled error.
func normalizeBackupCode(code string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	var b strings.Builder
	for _, r := range trimmed {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	stripped := b.String()
	if len([]rune(stripped)) != 8 {
		return trimmed
	}
	runes := []rune(stripped)
	return string(runes[:4]) + "-" + string(runes[4:])
}

// ---------------------------------------------------------------------------
// Anti-replay: a (user, code) pair may pass verification only once inside the
// validity window. Redis-backed when available (multi-replica safe), otherwise
// a process-local map.
// ---------------------------------------------------------------------------

type memEntry struct{ expiresAt time.Time }

var (
	memMu     sync.Mutex
	memUsed   = map[string]memEntry{}
	memFails  = map[string][]time.Time{}
	timeNowFn = time.Now // test seam
)

func sweepLocked(now time.Time) {
	for k, v := range memUsed {
		if now.After(v.expiresAt) {
			delete(memUsed, k)
		}
	}
	for k, ts := range memFails {
		kept := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < failWindow {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(memFails, k)
		} else {
			memFails[k] = kept
		}
	}
}

// MarkCodeUsed atomically records the (user, code) consumption. It returns
// true when this is the first use inside the replay window; false means the
// code was already spent and MUST be rejected.
// The code itself is only stored as an HMAC, never verbatim.
func MarkCodeUsed(ctx context.Context, userId int, code string) bool {
	digest := common.GenerateHMAC(strconv.Itoa(userId) + ":" + strings.TrimSpace(code))
	if common.RedisEnabled {
		ok, err := common.RDB.SetNX(ctx, "totp:used:"+digest, "1", replayTTL).Result()
		if err != nil {
			common.SysError("totp replay-guard redis error: " + err.Error())
			return false // fail closed: cannot prove first use
		}
		return ok
	}
	now := timeNowFn()
	memMu.Lock()
	defer memMu.Unlock()
	sweepLocked(now)
	if _, exists := memUsed[digest]; exists {
		return false
	}
	memUsed[digest] = memEntry{expiresAt: now.Add(replayTTL)}
	return true
}

// ---------------------------------------------------------------------------
// Per-user failure throttling (anti brute-force): at most failLimit wrong
// codes per failWindow per user, independent of source IP.
// ---------------------------------------------------------------------------

func failKey(userId int) string { return "totp:fail:" + strconv.Itoa(userId) }

// AllowAttempt reports whether the user is still under the failure budget.
//
// Two distinct Redis outcomes are NOT the same thing (cycle-12 L4, proved by
// throttle_backend_error_test.go):
//
//	redis.Nil  — the key genuinely does not exist, i.e. this user has no
//	             recorded failures. Admit, and do not consult the local
//	             counter: a successful verification on another replica
//	             clears the Redis key, and answering out of a stale local
//	             counter would keep refusing a user who is already clear.
//	other err  — the backend is unreachable/erroring. Fall back to the
//	             process-local counter. That is a per-replica ceiling rather
//	             than the cluster-wide one, but the previous behaviour
//	             (admit) removed the brute-force ceiling on TOTP
//	             verification entirely for the length of the outage.
func AllowAttempt(ctx context.Context, userId int) bool {
	if common.RedisEnabled {
		v, err := common.RDB.Get(ctx, failKey(userId)).Result()
		if errors.Is(err, redis.Nil) {
			return true
		}
		if err != nil {
			common.SysError("totp fail-counter redis read error, falling back to the process-local counter: " + err.Error())
			return allowAttemptFromMemory(userId)
		}
		n, _ := strconv.Atoi(v)
		return n < failLimit
	}
	return allowAttemptFromMemory(userId)
}

func allowAttemptFromMemory(userId int) bool {
	now := timeNowFn()
	memMu.Lock()
	defer memMu.Unlock()
	sweepLocked(now)
	return len(memFails[failKey(userId)]) < failLimit
}

// RecordFailure counts one wrong-code attempt for the user. A Redis error
// routes the count to the process-local counter instead of dropping it —
// otherwise AllowAttempt's fallback above would have nothing to read and the
// budget would still be unbounded during an outage.
func RecordFailure(ctx context.Context, userId int) {
	if common.RedisEnabled {
		key := failKey(userId)
		n, err := common.RDB.Incr(ctx, key).Result()
		if err != nil {
			common.SysError("totp fail-counter redis write error, counting in-process instead: " + err.Error())
			recordFailureInMemory(userId)
			return
		}
		if n == 1 {
			common.RDB.Expire(ctx, key, failWindow)
		}
		return
	}
	recordFailureInMemory(userId)
}

func recordFailureInMemory(userId int) {
	now := timeNowFn()
	memMu.Lock()
	defer memMu.Unlock()
	memFails[failKey(userId)] = append(memFails[failKey(userId)], now)
}

// ClearFailures resets the failure budget after a successful verification.
// It drops the process-local counter on every backend, not just the
// memory-only one: entries land there whenever RecordFailure hits a Redis
// error, and leaving them behind would charge a later outage's budget with
// failures this verification already forgave.
func ClearFailures(ctx context.Context, userId int) {
	memMu.Lock()
	delete(memFails, failKey(userId))
	memMu.Unlock()

	if common.RedisEnabled {
		if err := common.RDB.Del(ctx, failKey(userId)).Err(); err != nil {
			common.SysError("totp fail-counter redis error: " + err.Error())
		}
	}
}

// ResetStateForTest clears process-local replay/failure state between tests.
func ResetStateForTest() {
	memMu.Lock()
	defer memMu.Unlock()
	memUsed = map[string]memEntry{}
	memFails = map[string][]time.Time{}
}
