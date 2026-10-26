package bifrosteng

import (
	"os"
	"strings"
	"sync"
)

const (
	EngineLegacy  = "legacy"
	EngineBifrost = "bifrost"

	ModeChat       = "chat"
	ModeEmbeddings = "embeddings"
	ModeMessages   = "messages"
)

var (
	engineOnce sync.Once
	engineVal  string
	modesOnce  sync.Once
	modesVal   map[string]bool
)

// Engine returns RELAY_ENGINE (default legacy). Values other than "bifrost"
// are treated as legacy so a typo never silently enables the new path.
func Engine() string {
	engineOnce.Do(func() {
		v := strings.ToLower(strings.TrimSpace(os.Getenv("RELAY_ENGINE")))
		if v == EngineBifrost {
			engineVal = EngineBifrost
			return
		}
		engineVal = EngineLegacy
	})
	return engineVal
}

// Modes returns the set of modalities Bifrost may handle when Engine=bifrost.
// RELAY_BIFROST_MODES is a comma-separated list; default is "chat".
func Modes() map[string]bool {
	modesOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("RELAY_BIFROST_MODES"))
		if raw == "" {
			raw = ModeChat
		}
		modesVal = make(map[string]bool)
		for _, part := range strings.Split(raw, ",") {
			m := strings.ToLower(strings.TrimSpace(part))
			if m != "" {
				modesVal[m] = true
			}
		}
	})
	return modesVal
}

// ModeEnabled reports whether modality is in RELAY_BIFROST_MODES.
func ModeEnabled(modality string) bool {
	return Modes()[strings.ToLower(strings.TrimSpace(modality))]
}

// Enabled is true when the Bifrost engine flag is on.
func Enabled() bool {
	return Engine() == EngineBifrost
}

// ResetConfigForTest clears cached env reads. Tests only.
func ResetConfigForTest() {
	engineOnce = sync.Once{}
	engineVal = ""
	modesOnce = sync.Once{}
	modesVal = nil
}
