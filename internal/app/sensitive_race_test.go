package app

import (
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
)

// sensitive_race_test.go — the sensitive-word list must not be observable
// half-built.
//
// SensitiveWordsFromString used to publish by emptying the live slice and then
// appending into it word by word, so every relay request that ran the prompt
// check during that window saw a shorter list — and, for the first instant,
// an empty one, which SensitiveWordContains short-circuits to "clean". The
// window opens on every SyncOptions tick (repo.loadOptionsFromDatabase feeds
// "SensitiveWords" through updateOptionMap), not only on an admin edit, so a
// filter an operator believes is on is off for a slice of every minute.
//
// The contract: a writer builds the new list privately and publishes it in one
// step, and readers take the accessor, so any single check sees one complete
// list.
func TestSensitiveWords_ConcurrentPublishNeverShowsAnEmptyList(t *testing.T) {
	previous := setting.SensitiveWordsToString()
	t.Cleanup(func() { setting.SensitiveWordsFromString(previous) })

	setting.SensitiveWordsFromString("alpha\nbravo\ncharlie\ndelta\necho")

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			setting.SensitiveWordsFromString("alpha\nbravo\ncharlie\ndelta\necho")
		}
	}()

	var missed int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if hit, _ := CheckSensitiveText("a message that contains alpha in the middle"); !hit {
				missed++
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	if missed != 0 {
		t.Fatalf("the sensitive-word check missed a banned word %d time(s) while the list was being republished", missed)
	}
}
