package logger

// log_file_retention_test.go — cycle-11 L2: log files under the data dir
// rotate (SetupLogger, via checkLogRotation) but were never pruned, so a
// long-lived pod (emptyDir, no persistent disk) grew one oneapi-*.log file
// per rotation forever. Drives the real SetupLogger, which calls
// pruneLogFiles internally, rather than calling pruneLogFiles as a
// stand-in for the behavior it is meant to pin.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestSetupLogger_PrunesRotatedFilesBeyondRetain(t *testing.T) {
	origDir := *common.LogDir
	tmpDir, err := os.MkdirTemp("", "logger-retain-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	*common.LogDir = tmpDir
	t.Setenv("LOG_FILE_RETAIN", "3")
	t.Cleanup(func() {
		*common.LogDir = origDir
		_ = os.RemoveAll(tmpDir) // best-effort; the just-created fd may still be held on Windows
	})

	// Seed 5 rotated files with sortable timestamps, all older than "now"
	// (the real timestamp SetupLogger will stamp its own new file with), so
	// the ordering after SetupLogger runs is unambiguous.
	seeded := []string{
		"oneapi-20260101000001.log",
		"oneapi-20260101000002.log",
		"oneapi-20260101000003.log",
		"oneapi-20260101000004.log",
		"oneapi-20260101000005.log",
	}
	for _, name := range seeded {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("old"), 0644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	SetupLogger() // creates a 6th (newest) file and must prune down to 3 total

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp log dir: %v", err)
	}
	var got []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "oneapi-") && strings.HasSuffix(e.Name(), ".log") {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)

	if len(got) != 3 {
		t.Fatalf("oneapi-*.log count = %d (%v), want exactly 3 (retain)", len(got), got)
	}
	// The two oldest survivors of the 6 total must be the newest two seeded
	// files; the oldest three seeded files must be gone.
	if got[0] != "oneapi-20260101000004.log" || got[1] != "oneapi-20260101000005.log" {
		t.Errorf("survivors = %v, want the 2 newest seeded files plus SetupLogger's own new file", got)
	}
	for _, removed := range seeded[:3] {
		for _, g := range got {
			if g == removed {
				t.Errorf("expected %s to be pruned, but it survived: %v", removed, got)
			}
		}
	}
}

// TestPruneLogFiles_NonPositiveKeepFallsBackToDefault pins pruneLogFiles'
// keep<=0 contract: 0 or a negative retain count does not mean "never
// prune" (which would let a long-lived pod's emptyDir grow one file per
// rotation forever, exactly the failure this function exists to prevent);
// it falls back to defaultLogFileRetain.
func TestPruneLogFiles_NonPositiveKeepFallsBackToDefault(t *testing.T) {
	seeded := []string{
		"oneapi-20260101000001.log",
		"oneapi-20260101000002.log",
		"oneapi-20260101000003.log",
		"oneapi-20260101000004.log",
		"oneapi-20260101000005.log",
	}

	for _, keep := range []int{0, -1} {
		t.Run(fmt.Sprintf("keep=%d", keep), func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "logger-prune-nonpositive-test-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

			for _, name := range seeded {
				if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("old"), 0644); err != nil {
					t.Fatalf("seed %s: %v", name, err)
				}
			}

			pruneLogFiles(tmpDir, keep)

			entries, err := os.ReadDir(tmpDir)
			if err != nil {
				t.Fatalf("failed to read temp log dir: %v", err)
			}
			var got []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "oneapi-") && strings.HasSuffix(e.Name(), ".log") {
					got = append(got, e.Name())
				}
			}
			sort.Strings(got)

			if len(got) != defaultLogFileRetain {
				t.Fatalf("keep=%d: oneapi-*.log count = %d (%v), want %d (fallback to default)", keep, len(got), got, defaultLogFileRetain)
			}
			if got[0] != "oneapi-20260101000003.log" {
				t.Errorf("keep=%d: survivors = %v, want the newest %d seeded files", keep, got, defaultLogFileRetain)
			}
		})
	}
}

// TestSetupLogger_ClosesPreviousFdAfterGrace pins the fd-close-after-grace
// half of the fix: without it, every rotation leaked the *os.File SetupLogger
// had just replaced (relevant on a long-lived pod: each rotation opened
// another fd that was never closed). Shrinks logFdCloseGrace so the test
// does not sleep the production 2s value.
func TestSetupLogger_ClosesPreviousFdAfterGrace(t *testing.T) {
	origDir := *common.LogDir
	origGrace := logFdCloseGrace
	tmpDir, err := os.MkdirTemp("", "logger-fdclose-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	*common.LogDir = tmpDir
	logFdCloseGrace = 10 * time.Millisecond
	t.Setenv("LOG_FILE_RETAIN", "3")
	t.Cleanup(func() {
		*common.LogDir = origDir
		logFdCloseGrace = origGrace
		_ = os.RemoveAll(tmpDir) // best-effort; a held fd may block removal on Windows
	})

	SetupLogger()
	firstFd := currentLogFd.Load()
	if firstFd == nil {
		t.Fatal("currentLogFd is nil after first SetupLogger call")
	}

	// Orders the two SetupLogger calls. The filename is second-granular
	// (logger.go formats "20060102150405"), so both calls may open the same
	// path — what matters here is that the second call swaps in a new fd.
	time.Sleep(time.Millisecond)
	SetupLogger()

	// The grace goroutine from the first call's swap closes firstFd after
	// logFdCloseGrace; wait comfortably past it.
	time.Sleep(200 * time.Millisecond)

	if _, err := firstFd.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("write to first fd after grace period: err = %v, want os.ErrClosed", err)
	}
}
