package lifecycle

// cov_logsink_test.go — one process-wide log sink for this package's tests.
//
// The launchers here spawn fire-and-forget goroutines (common.SafeGoWithContext)
// that call common.SysLog, which reads gin.DefaultWriter (sys_log.go:19). Those
// goroutines outlive the test that started them — a cancelled privacy-erasure
// executor still logs "privacy erasure executor stopped" on its way out — so any
// test that swaps gin.DefaultWriter races them on the interface variable itself.
// A mutex-guarded buffer does not help there: the race is on the global, not on
// what it points at.
//
// So the swap happens exactly once, before any test runs, and no test ever
// changes it again. Tests that need to assert on log output record the sink's
// length first and read the suffix.

import (
	"bytes"
	"os"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

type covLogSinkBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *covLogSinkBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// mark returns the current length, for use as the start of a later read.
func (b *covLogSinkBuffer) mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// since returns everything written after mark. Lines from other tests'
// background goroutines may be interleaved, which is fine for substring
// assertions.
func (b *covLogSinkBuffer) since(mark int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if mark < 0 || mark > len(s) {
		return s
	}
	return s[mark:]
}

var covLogSink = &covLogSinkBuffer{}

func TestMain(m *testing.M) {
	gin.DefaultWriter = covLogSink
	os.Exit(m.Run())
}
