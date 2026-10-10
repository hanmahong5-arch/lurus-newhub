package repo

import (
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// log_persisted_hook.go - extension point for consumers that must react to a
// log row that was just stored (the data-event lane). It is deliberately an
// empty seam: nothing registers by default, so with no hook the cost is one
// read-locked slice read per log write and behaviour is unchanged.

// LogPersistedHook receives the row exactly as stored (after the retention
// chokepoint trimmed it), once per successful insert of a consume or error
// log. It runs synchronously on the logging goroutine, so it MUST return
// quickly (hand off to a queue), must not retain or mutate l, and must not
// touch the request's gin context. A panic is recovered and logged; it never
// reaches the billing or log path.
type LogPersistedHook func(l *Log)

var (
	logPersistedMu    sync.RWMutex
	logPersistedHooks []LogPersistedHook
)

// OnLogPersisted registers a hook; call it at boot. Registration order is
// invocation order.
func OnLogPersisted(h LogPersistedHook) {
	if h == nil {
		return
	}
	logPersistedMu.Lock()
	logPersistedHooks = append(logPersistedHooks, h)
	logPersistedMu.Unlock()
}

// resetLogPersistedHooks removes every hook (tests only).
func resetLogPersistedHooks() {
	logPersistedMu.Lock()
	logPersistedHooks = nil
	logPersistedMu.Unlock()
}

func notifyLogPersisted(l *Log) {
	logPersistedMu.RLock()
	hooks := logPersistedHooks
	logPersistedMu.RUnlock()
	for _, h := range hooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError("log persisted hook panicked")
				}
			}()
			h(l)
		}()
	}
}
