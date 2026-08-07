package app

// cov_core-app-boot_token_service_test.go — coverage for token_service.go
// functions left at 0%/low: ValidateRateLimits (RPM/TPM bounds validation)
// and GenerateTokenKey (the real key-generation path, as opposed to the
// zero-arg constructors already exercised elsewhere).

import (
	"strings"
	"testing"
)

func TestCoreAppBootValidateRateLimits(t *testing.T) {
	tests := []struct {
		name    string
		rpm     int
		tpm     int
		wantErr bool
	}{
		{"both_zero_unlimited", 0, 0, false},
		{"typical_positive_values", 60, 100000, false},
		{"rpm_at_max", MaxRateLimitPerMinute, 0, false},
		{"tpm_at_max", 0, MaxRateLimitPerMinute, false},
		{"rpm_negative", -1, 0, true},
		{"tpm_negative", 0, -1, true},
		{"both_negative", -5, -5, true},
		{"rpm_over_max", MaxRateLimitPerMinute + 1, 0, true},
		{"tpm_over_max", 0, MaxRateLimitPerMinute + 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRateLimits(tc.rpm, tc.tpm)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateRateLimits(%d, %d) = nil, want error", tc.rpm, tc.tpm)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateRateLimits(%d, %d) = %v, want nil", tc.rpm, tc.tpm, err)
			}
		})
	}
}

// TestCoreAppBootGenerateTokenKey_ReturnsDistinctNonEmptyKeys exercises the
// real key generation path (common.GenerateKey under the hood) and asserts
// the business contract: non-empty, and two consecutive calls must not
// collide (this is the actual bearer secret minted for API tokens).
func TestCoreAppBootGenerateTokenKey_ReturnsDistinctNonEmptyKeys(t *testing.T) {
	key1, err := GenerateTokenKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key1 == "" {
		t.Fatal("expected a non-empty token key")
	}
	if strings.TrimSpace(key1) != key1 {
		t.Errorf("expected no leading/trailing whitespace in generated key, got %q", key1)
	}

	key2, err := GenerateTokenKey()
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if key1 == key2 {
		t.Fatal("expected two consecutive GenerateTokenKey calls to produce distinct keys")
	}
}
