package common

import "testing"

// TestInstanceID pins the precedence POD_NAME > HOSTNAME > "node" — the
// downward API value must win over whatever HOSTNAME the container runtime
// happens to set, and both must beat the bare fallback used by local dev.
func TestInstanceID(t *testing.T) {
	tests := []struct {
		name     string
		podName  string
		hostname string
		want     string
	}{
		{"POD_NAME wins over HOSTNAME", "pod-a", "host-b", "pod-a"},
		{"HOSTNAME used when POD_NAME unset", "", "host-b", "host-b"},
		{"falls back to node when both unset", "", "", "node"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.podName == "" {
				t.Setenv("POD_NAME", "")
			} else {
				t.Setenv("POD_NAME", tc.podName)
			}
			if tc.hostname == "" {
				t.Setenv("HOSTNAME", "")
			} else {
				t.Setenv("HOSTNAME", tc.hostname)
			}
			if got := InstanceID(); got != tc.want {
				t.Errorf("InstanceID() = %q, want %q", got, tc.want)
			}
		})
	}
}
