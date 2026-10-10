package constant

import "testing"

// ChannelBaseURLs is index-aligned with the ChannelType* constants: one entry
// short or one misplaced and every later type silently inherits a neighbour's
// default base URL. Dummy is the count, so the slice length must equal it.
func TestChannelBaseURLs_AlignedWithChannelTypes(t *testing.T) {
	if len(ChannelBaseURLs) != ChannelTypeDummy {
		t.Fatalf("len(ChannelBaseURLs) = %d, want ChannelTypeDummy = %d", len(ChannelBaseURLs), ChannelTypeDummy)
	}
}

func TestSystemOneChannelTypes(t *testing.T) {
	if ChannelTypeTypeSafe != 57 || ChannelTypeSystemOneCompatible != 58 || ChannelTypeDummy != 60 {
		t.Fatalf("channel ids = %d/%d/%d, want 57/58/59 appended before Dummy",
			ChannelTypeTypeSafe, ChannelTypeSystemOneCompatible, ChannelTypeDummy)
	}
	if got := ChannelBaseURLs[ChannelTypeTypeSafe]; got != "https://api.typesafe.ai" {
		t.Errorf("TypeSafe default base = %q", got)
	}
	// Self-hosted: the operator must supply the address, no default.
	if got := ChannelBaseURLs[ChannelTypeSystemOneCompatible]; got != "" {
		t.Errorf("compatible default base = %q, want empty", got)
	}
	if got := GetChannelTypeName(ChannelTypeTypeSafe); got != "TypeSafe" {
		t.Errorf("name = %q", got)
	}
	if got := GetChannelTypeName(ChannelTypeSystemOneCompatible); got != "System One compatible (self-hosted)" {
		t.Errorf("name = %q", got)
	}
	if EndpointTypeSystemOne != "systemone" {
		t.Errorf("EndpointTypeSystemOne = %q", EndpointTypeSystemOne)
	}
	if APITypeSystemOne != APITypeDummy-2 {
		t.Errorf("APITypeSystemOne = %d, want the slot two before APITypeDummy (Voyage follows) (%d)", APITypeSystemOne, APITypeDummy)
	}
}
