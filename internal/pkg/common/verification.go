package common

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type verificationValue struct {
	code string
	time time.Time
}

const (
	EmailVerificationPurpose = "v"
	PasswordResetPurpose     = "r"
	PhoneLoginPurpose        = "pl"
	PhoneRegisterPurpose     = "pr"
	PhoneBindPurpose         = "pb"
	PhoneResetPurpose        = "prs"
)

var verificationMutex sync.Mutex
var verificationMap map[string]verificationValue
var verificationMapMaxSize = 10
var VerificationValidMinutes = 10

func GenerateVerificationCode(length int) string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	if length == 0 {
		return code
	}
	return code[:length]
}

func RegisterVerificationCodeWithKey(key string, code string, purpose string) {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationValue{
		code: code,
		time: time.Now(),
	}
	if len(verificationMap) > verificationMapMaxSize {
		removeExpiredPairs()
	}
}

func VerifyCodeWithKey(key string, code string, purpose string) bool {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, okay := verificationMap[purpose+key]
	now := time.Now()
	if !okay || int(now.Sub(value.time).Seconds()) >= VerificationValidMinutes*60 {
		return false
	}
	return code == value.code
}

func DeleteKey(key string, purpose string) {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

// ConsumeCodeWithKey atomically verifies the code and, on success, deletes it
// within a single lock hold (one-time-use semantics). Unlike calling
// VerifyCodeWithKey followed by DeleteKey — which releases the mutex between
// check and delete — there is no window for a concurrent caller to verify the
// same code twice: exactly one of N concurrent callers can succeed.
func ConsumeCodeWithKey(key string, code string, purpose string) bool {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, okay := verificationMap[purpose+key]
	now := time.Now()
	if !okay || int(now.Sub(value.time).Seconds()) >= VerificationValidMinutes*60 {
		return false
	}
	if code != value.code {
		return false
	}
	delete(verificationMap, purpose+key)
	return true
}

// no lock inside, so the caller must lock the verificationMap before calling!
func removeExpiredPairs() {
	now := time.Now()
	for key := range verificationMap {
		if int(now.Sub(verificationMap[key].time).Seconds()) >= VerificationValidMinutes*60 {
			delete(verificationMap, key)
		}
	}
}

func init() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationValue)
}

// Phone verification helper functions

// GeneratePhoneVerificationCode generates a 6-digit numeric code for phone verification
func GeneratePhoneVerificationCode() string {
	return GenerateSmsCode()
}

// RegisterPhoneVerificationCode stores a verification code for a phone number
func RegisterPhoneVerificationCode(phone string, code string, purpose string) {
	RegisterVerificationCodeWithKey(phone, code, purpose)
}

// VerifyPhoneCode verifies and deletes the code if correct (one-time use).
// Verification and deletion happen atomically under one lock hold via
// ConsumeCodeWithKey, so concurrent requests with the same code cannot both
// succeed (the old check-then-delete sequence had a TOCTOU window).
func VerifyPhoneCode(phone string, code string, purpose string) bool {
	return ConsumeCodeWithKey(phone, code, purpose)
}

// GetPhonePurpose converts purpose string to constant
func GetPhonePurpose(purpose string) string {
	switch purpose {
	case "login":
		return PhoneLoginPurpose
	case "register":
		return PhoneRegisterPurpose
	case "bind":
		return PhoneBindPurpose
	case "reset":
		return PhoneResetPurpose
	default:
		return PhoneLoginPurpose
	}
}
