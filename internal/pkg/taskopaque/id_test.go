package taskopaque

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	prev := common.CryptoSecret
	common.CryptoSecret = "test-crypto-secret-for-opaque"
	t.Cleanup(func() { common.CryptoSecret = prev })

	got := Encode(42)
	if !stringsHasPrefix(got, "tht_") {
		t.Fatalf("encode = %q", got)
	}
	id, ok := Decode(got)
	if !ok || id != 42 {
		t.Fatalf("decode = %d,%v", id, ok)
	}
}

func TestDecodeRejectsTamper(t *testing.T) {
	prev := common.CryptoSecret
	common.CryptoSecret = "test-crypto-secret-for-opaque"
	t.Cleanup(func() { common.CryptoSecret = prev })

	got := Encode(7)
	tampered := got[:len(got)-1] + "x"
	if _, ok := Decode(tampered); ok {
		t.Fatal("tampered opaque accepted")
	}
	if _, ok := Decode("sora"); ok {
		t.Fatal("platform name must not decode")
	}
	if _, ok := Decode(""); ok {
		t.Fatal("empty accepted")
	}
}

func stringsHasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
