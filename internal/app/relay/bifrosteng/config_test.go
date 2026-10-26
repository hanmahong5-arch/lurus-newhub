package bifrosteng

import (
	"testing"
)

func TestEngineDefaultLegacy(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "")
	ResetConfigForTest()
	if got := Engine(); got != EngineLegacy {
		t.Fatalf("Engine()=%q, want %q", got, EngineLegacy)
	}
}

func TestEngineBifrost(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "bifrost")
	ResetConfigForTest()
	if got := Engine(); got != EngineBifrost {
		t.Fatalf("Engine()=%q, want %q", got, EngineBifrost)
	}
}

func TestEngineTypoIsLegacy(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "BifrostX")
	ResetConfigForTest()
	if got := Engine(); got != EngineLegacy {
		t.Fatalf("Engine()=%q, want legacy for typo", got)
	}
}

func TestModesDefaultChat(t *testing.T) {
	t.Setenv("RELAY_BIFROST_MODES", "")
	ResetConfigForTest()
	if !ModeEnabled(ModeChat) {
		t.Fatal("expected chat mode enabled by default")
	}
	if ModeEnabled(ModeEmbeddings) {
		t.Fatal("embeddings should be off by default")
	}
}

func TestModesCSV(t *testing.T) {
	t.Setenv("RELAY_BIFROST_MODES", "chat, embeddings")
	ResetConfigForTest()
	if !ModeEnabled(ModeChat) || !ModeEnabled(ModeEmbeddings) {
		t.Fatalf("modes=%v", Modes())
	}
}
