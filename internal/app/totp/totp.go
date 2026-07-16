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
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/pquerna/otp"
	pqtotp "github.com/pquerna/otp/totp"
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
func AllowAttempt(ctx context.Context, userId int) bool {
	if common.RedisEnabled {
		v, err := common.RDB.Get(ctx, failKey(userId)).Result()
		if err != nil { // key missing or redis error → allow (limiter is best-effort)
			return true
		}
		n, _ := strconv.Atoi(v)
		return n < failLimit
	}
	now := timeNowFn()
	memMu.Lock()
	defer memMu.Unlock()
	sweepLocked(now)
	return len(memFails[failKey(userId)]) < failLimit
}

// RecordFailure counts one wrong-code attempt for the user.
func RecordFailure(ctx context.Context, userId int) {
	if common.RedisEnabled {
		key := failKey(userId)
		n, err := common.RDB.Incr(ctx, key).Result()
		if err != nil {
			common.SysError("totp fail-counter redis error: " + err.Error())
			return
		}
		if n == 1 {
			common.RDB.Expire(ctx, key, failWindow)
		}
		return
	}
	now := timeNowFn()
	memMu.Lock()
	defer memMu.Unlock()
	memFails[failKey(userId)] = append(memFails[failKey(userId)], now)
}

// ClearFailures resets the failure budget after a successful verification.
func ClearFailures(ctx context.Context, userId int) {
	if common.RedisEnabled {
		if err := common.RDB.Del(ctx, failKey(userId)).Err(); err != nil {
			common.SysError("totp fail-counter redis error: " + err.Error())
		}
		return
	}
	memMu.Lock()
	defer memMu.Unlock()
	delete(memFails, failKey(userId))
}

// ResetStateForTest clears process-local replay/failure state between tests.
func ResetStateForTest() {
	memMu.Lock()
	defer memMu.Unlock()
	memUsed = map[string]memEntry{}
	memFails = map[string][]time.Time{}
}
