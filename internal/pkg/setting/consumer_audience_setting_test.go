package setting

import "testing"

// The one property that must never break: no input may yield "enforce" unless
// it says so exactly. A typo that silently enforced would 401 every consumer
// caller of the gate, which is the failure mode this flag exists to avoid.
func TestGetConsumerAudRequired(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"unset defaults to log", "", ConsumerAudRequiredLog},
		{"explicit log", ConsumerAudRequiredLog, ConsumerAudRequiredLog},
		{"explicit off", ConsumerAudRequiredOff, ConsumerAudRequiredOff},
		{"explicit enforce", ConsumerAudRequiredEnforce, ConsumerAudRequiredEnforce},
		{"typo degrades to log", "enfroce", ConsumerAudRequiredLog},
		{"case mismatch degrades to log", "ENFORCE", ConsumerAudRequiredLog},
		{"whitespace is not trimmed away into enforce", " enforce ", ConsumerAudRequiredLog},
		{"unrelated value degrades to log", "true", ConsumerAudRequiredLog},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OIDC_CONSUMER_AUD_REQUIRED", tc.env)
			if got := GetConsumerAudRequired(); got != tc.want {
				t.Fatalf("GetConsumerAudRequired() with env %q = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}
