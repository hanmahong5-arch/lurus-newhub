package setting

import "testing"

// TestGetCreditPoolRequired covers the CREDIT_POOL_REQUIRED three-state
// parser directly: valid values pass through, empty/unset defaults to off,
// and any unrecognized value degrades to off rather than fail-opening to
// enforce (see pool_balance_check.go for the consumer).
func TestGetCreditPoolRequired(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"unset defaults to off", "", CreditPoolRequiredOff},
		{"explicit off", CreditPoolRequiredOff, CreditPoolRequiredOff},
		{"log", CreditPoolRequiredLog, CreditPoolRequiredLog},
		{"enforce", CreditPoolRequiredEnforce, CreditPoolRequiredEnforce},
		{"unknown value degrades to off", "banana", CreditPoolRequiredOff},
		{"case-sensitive: ENFORCE is unknown, degrades to off", "ENFORCE", CreditPoolRequiredOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CREDIT_POOL_REQUIRED", tc.env)
			if got := GetCreditPoolRequired(); got != tc.want {
				t.Errorf("GetCreditPoolRequired() with env=%q = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}
