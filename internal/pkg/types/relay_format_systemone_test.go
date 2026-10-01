package types

import "testing"

func TestRelayFormatSystemOne(t *testing.T) {
	if RelayFormatSystemOne != "systemone" {
		t.Fatalf("RelayFormatSystemOne = %q, want systemone", RelayFormatSystemOne)
	}
}
