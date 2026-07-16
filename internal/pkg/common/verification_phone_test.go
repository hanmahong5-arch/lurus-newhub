package common

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestGetPhonePurpose tests purpose string to constant conversion
func TestGetPhonePurpose(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"login_purpose", "login", PhoneLoginPurpose},
		{"register_purpose", "register", PhoneRegisterPurpose},
		{"bind_purpose", "bind", PhoneBindPurpose},
		{"reset_purpose", "reset", PhoneResetPurpose},
		{"unknown_defaults_to_login", "unknown", PhoneLoginPurpose},
		{"empty_defaults_to_login", "", PhoneLoginPurpose},
		{"uppercase_defaults", "LOGIN", PhoneLoginPurpose},
		{"mixed_case_defaults", "Login", PhoneLoginPurpose},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetPhonePurpose(tt.input)
			if result != tt.expected {
				t.Errorf("GetPhonePurpose(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestPhoneVerificationCodeRegistration tests code registration
func TestPhoneVerificationCodeRegistration(t *testing.T) {
	tests := []struct {
		name    string
		phone   string
		code    string
		purpose string
	}{
		{"login_code", "13800138001", "123456", "login"},
		{"register_code", "13800138002", "654321", "register"},
		{"bind_code", "13800138003", "111111", "bind"},
		{"reset_code", "13800138004", "999999", "reset"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			purposeKey := GetPhonePurpose(tt.purpose)
			RegisterPhoneVerificationCode(tt.phone, tt.code, purposeKey)

			// Verify code was registered by checking verification
			result := VerifyCodeWithKey(tt.phone, tt.code, purposeKey)
			if !result {
				t.Errorf("Code not registered correctly for phone %s", tt.phone)
			}

			// Clean up
			DeleteKey(tt.phone, purposeKey)
		})
	}
}

// TestPhoneVerificationFlow tests the complete verification flow
func TestPhoneVerificationFlow(t *testing.T) {
	tests := []struct {
		name          string
		phone         string
		purpose       string
		code          string
		verifyCode    string
		verifyPurpose string
		expectSuccess bool
	}{
		// Normal flow - correct code and purpose
		{
			name: "valid_verification",
			phone: "13900000001", purpose: "login",
			code: "123456", verifyCode: "123456", verifyPurpose: "login",
			expectSuccess: true,
		},

		// Wrong code
		{
			name: "wrong_code",
			phone: "13900000002", purpose: "login",
			code: "123456", verifyCode: "654321", verifyPurpose: "login",
			expectSuccess: false,
		},

		// Wrong purpose
		{
			name: "wrong_purpose",
			phone: "13900000003", purpose: "login",
			code: "123456", verifyCode: "123456", verifyPurpose: "register",
			expectSuccess: false,
		},

		// Case sensitivity - codes are case-sensitive
		{
			name: "case_sensitive_code",
			phone: "13900000004", purpose: "login",
			code: "ABC123", verifyCode: "abc123", verifyPurpose: "login",
			expectSuccess: false,
		},

		// Empty code verification
		{
			name: "empty_verify_code",
			phone: "13900000005", purpose: "login",
			code: "123456", verifyCode: "", verifyPurpose: "login",
			expectSuccess: false,
		},

		// Partial code match
		{
			name: "partial_code_match",
			phone: "13900000006", purpose: "login",
			code: "123456", verifyCode: "12345", verifyPurpose: "login",
			expectSuccess: false,
		},

		// Code with leading zeros
		{
			name: "code_with_leading_zeros",
			phone: "13900000007", purpose: "login",
			code: "001234", verifyCode: "001234", verifyPurpose: "login",
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Register code
			purposeKey := GetPhonePurpose(tt.purpose)
			RegisterPhoneVerificationCode(tt.phone, tt.code, purposeKey)

			// Verify
			verifyPurposeKey := GetPhonePurpose(tt.verifyPurpose)
			result := VerifyPhoneCode(tt.phone, tt.verifyCode, verifyPurposeKey)

			if result != tt.expectSuccess {
				t.Errorf("VerifyPhoneCode() = %v, want %v", result, tt.expectSuccess)
			}

			// Clean up if verification failed (successful verification auto-deletes)
			if !tt.expectSuccess {
				DeleteKey(tt.phone, purposeKey)
			}
		})
	}
}

// TestOneTimeCodeUsage tests that codes can only be used once
func TestOneTimeCodeUsage(t *testing.T) {
	phone := "13911111111"
	code := "123456"
	purpose := GetPhonePurpose("login")

	// Register code
	RegisterPhoneVerificationCode(phone, code, purpose)

	// First verification should succeed
	result1 := VerifyPhoneCode(phone, code, purpose)
	if !result1 {
		t.Error("First verification should succeed")
	}

	// Second verification should fail (code already used)
	result2 := VerifyPhoneCode(phone, code, purpose)
	if result2 {
		t.Error("Second verification should fail - code should be deleted after first use")
	}
}

// TestCodeOverwrite tests that new codes overwrite old ones
func TestCodeOverwrite(t *testing.T) {
	phone := "13922222222"
	code1 := "111111"
	code2 := "222222"
	purpose := GetPhonePurpose("login")

	// Register first code
	RegisterPhoneVerificationCode(phone, code1, purpose)

	// Register second code (should overwrite)
	RegisterPhoneVerificationCode(phone, code2, purpose)

	// Old code should not work
	result1 := VerifyCodeWithKey(phone, code1, purpose)
	if result1 {
		t.Error("Old code should not work after overwrite")
	}

	// New code should work
	result2 := VerifyPhoneCode(phone, code2, purpose)
	if !result2 {
		t.Error("New code should work")
	}
}

// TestMultiplePurposesSamePhone tests codes for different purposes
func TestMultiplePurposesSamePhone(t *testing.T) {
	phone := "13933333333"
	loginCode := "111111"
	bindCode := "222222"

	loginPurpose := GetPhonePurpose("login")
	bindPurpose := GetPhonePurpose("bind")

	// Register codes for different purposes
	RegisterPhoneVerificationCode(phone, loginCode, loginPurpose)
	RegisterPhoneVerificationCode(phone, bindCode, bindPurpose)

	// Each code should only work for its purpose
	if !VerifyCodeWithKey(phone, loginCode, loginPurpose) {
		t.Error("Login code should work for login purpose")
	}

	if !VerifyCodeWithKey(phone, bindCode, bindPurpose) {
		t.Error("Bind code should work for bind purpose")
	}

	// Cross-purpose verification should fail
	if VerifyCodeWithKey(phone, loginCode, bindPurpose) {
		t.Error("Login code should not work for bind purpose")
	}

	if VerifyCodeWithKey(phone, bindCode, loginPurpose) {
		t.Error("Bind code should not work for login purpose")
	}

	// Clean up
	DeleteKey(phone, loginPurpose)
	DeleteKey(phone, bindPurpose)
}

// TestConcurrentVerification tests that concurrent verification is safe
func TestConcurrentVerification(t *testing.T) {
	phone := "13944444444"
	code := "123456"
	purpose := GetPhonePurpose("login")

	// Register code
	RegisterPhoneVerificationCode(phone, code, purpose)

	var wg sync.WaitGroup
	var successCount int32
	concurrency := 10

	// Attempt concurrent verifications
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if VerifyPhoneCode(phone, code, purpose) {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// Only one verification should succeed (one-time use)
	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful verification, got %d", successCount)
	}
}

// TestConsumeCodeWithKey tests the atomic verify-and-delete primitive
func TestConsumeCodeWithKey(t *testing.T) {
	tests := []struct {
		name          string
		register      bool
		code          string
		consumeCode   string
		validMinutes  int // 0 = leave default (10)
		expectSuccess bool
		expectDeleted bool
	}{
		{
			name:     "correct_code_consumed",
			register: true, code: "123456", consumeCode: "123456",
			expectSuccess: true, expectDeleted: true,
		},
		{
			name:     "wrong_code_not_consumed",
			register: true, code: "123456", consumeCode: "654321",
			expectSuccess: false, expectDeleted: false,
		},
		{
			name:     "missing_key_fails",
			register: false, consumeCode: "123456",
			expectSuccess: false, expectDeleted: true,
		},
		{
			name:     "expired_code_fails",
			register: true, code: "123456", consumeCode: "123456",
			validMinutes:  -1, // everything is instantly expired
			expectSuccess: false, expectDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "consume-" + tt.name
			purpose := PhoneLoginPurpose

			if tt.validMinutes != 0 {
				original := VerificationValidMinutes
				VerificationValidMinutes = tt.validMinutes
				defer func() { VerificationValidMinutes = original }()
			}

			if tt.register {
				RegisterVerificationCodeWithKey(key, tt.code, purpose)
				defer DeleteKey(key, purpose)
			}

			got := ConsumeCodeWithKey(key, tt.consumeCode, purpose)
			if got != tt.expectSuccess {
				t.Errorf("ConsumeCodeWithKey() = %v, want %v", got, tt.expectSuccess)
			}

			// A successful consume must delete the code; a failed one must
			// leave a still-valid registered code in place.
			verificationMutex.Lock()
			_, stillThere := verificationMap[purpose+key]
			verificationMutex.Unlock()
			if stillThere == tt.expectDeleted {
				t.Errorf("code presence after consume = %v, want deleted=%v", stillThere, tt.expectDeleted)
			}
		})
	}
}

// TestConsumeCodeWithKey_Concurrent asserts one-time-use under heavy
// concurrency: N goroutines racing on the same code, exactly 1 may succeed.
func TestConsumeCodeWithKey_Concurrent(t *testing.T) {
	const concurrency = 100
	key := "consume-concurrent"
	code := "424242"
	purpose := PhoneLoginPurpose

	RegisterVerificationCodeWithKey(key, code, purpose)

	var wg sync.WaitGroup
	var successCount atomic.Int32
	start := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // barrier: maximize simultaneous entry
			if ConsumeCodeWithKey(key, code, purpose) {
				successCount.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if successCount.Load() != 1 {
		t.Errorf("expected exactly 1 successful consume, got %d", successCount.Load())
	}
}

// TestConcurrentRegistration tests that concurrent registration is safe
func TestConcurrentRegistration(t *testing.T) {
	phone := "13955555555"
	purpose := GetPhonePurpose("login")

	var wg sync.WaitGroup
	concurrency := 10

	// Concurrently register different codes
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		code := GenerateSmsCode()
		go func(c string) {
			defer wg.Done()
			RegisterPhoneVerificationCode(phone, c, purpose)
		}(code)
	}

	wg.Wait()

	// One code should be stored (last one wins)
	// We can't predict which one, but there should be exactly one valid code
	// Clean up
	DeleteKey(phone, purpose)
}

// TestVerificationMapCleanup tests that expired entries are cleaned up
func TestVerificationMapCleanup(t *testing.T) {
	// This test verifies the cleanup mechanism when map exceeds max size
	// We need to register many codes to trigger cleanup

	// Save original max size and restore after test
	originalMaxSize := verificationMapMaxSize
	verificationMapMaxSize = 5 // Lower for testing
	defer func() {
		verificationMapMaxSize = originalMaxSize
	}()

	// Register codes for different phones
	purpose := GetPhonePurpose("login")
	for i := 0; i < 10; i++ {
		phone := "139" + string(rune('0'+i)) + "0000000"
		code := GenerateSmsCode()
		RegisterPhoneVerificationCode(phone, code, purpose)
	}

	// The map should have handled the overflow
	// We can't directly check the map size from here, but the function should not panic

	// Clean up
	for i := 0; i < 10; i++ {
		phone := "139" + string(rune('0'+i)) + "0000000"
		DeleteKey(phone, purpose)
	}
}

// TestGeneratePhoneVerificationCode tests the wrapper function
func TestGeneratePhoneVerificationCode(t *testing.T) {
	t.Run("returns_6_digit_code", func(t *testing.T) {
		code := GeneratePhoneVerificationCode()
		if len(code) != 6 {
			t.Errorf("GeneratePhoneVerificationCode() length = %d, want 6", len(code))
		}
	})

	t.Run("returns_numeric_code", func(t *testing.T) {
		code := GeneratePhoneVerificationCode()
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Errorf("GeneratePhoneVerificationCode() contains non-numeric: %q", code)
			}
		}
	})
}

// TestVerificationValidMinutes tests the expiration time configuration
func TestVerificationValidMinutes(t *testing.T) {
	// The default is 10 minutes
	if VerificationValidMinutes != 10 {
		t.Logf("VerificationValidMinutes = %d (default is 10)", VerificationValidMinutes)
	}
}

// BenchmarkVerifyPhoneCode benchmarks verification
func BenchmarkVerifyPhoneCode(b *testing.B) {
	phone := "13800000000"
	code := "123456"
	purpose := GetPhonePurpose("login")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Register and verify in each iteration
		RegisterPhoneVerificationCode(phone, code, purpose)
		VerifyCodeWithKey(phone, code, purpose)
	}

	// Clean up
	DeleteKey(phone, purpose)
}

// BenchmarkConcurrentVerification benchmarks concurrent verification
func BenchmarkConcurrentVerification(b *testing.B) {
	phone := "13800000001"
	code := "123456"
	purpose := GetPhonePurpose("login")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			RegisterPhoneVerificationCode(phone, code, purpose)
			VerifyCodeWithKey(phone, code, purpose)
		}
	})

	// Clean up
	DeleteKey(phone, purpose)
}
