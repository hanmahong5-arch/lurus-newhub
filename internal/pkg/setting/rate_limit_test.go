package setting

import (
	"strconv"
	"sync"
	"testing"
)

// TestUpdateModelRequestRateLimitGroupByJSONString_SequentialCorrectness locks
// down write-then-read behavior: after an Update, GetGroupRateLimit must
// observe the new values, and a group that was never set must report
// found=false. This must hold regardless of which lock type the setter
// takes, so it does not by itself prove the concurrency bug is fixed — see
// TestConcurrentUpdateAndGet_NoFatalCrash below for that.
func TestUpdateModelRequestRateLimitGroupByJSONString_SequentialCorrectness(t *testing.T) {
	if err := UpdateModelRequestRateLimitGroupByJSONString(`{"default":[10,5]}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	total, success, found := GetGroupRateLimit("default")
	if !found {
		t.Fatalf("expected group 'default' to be found after update")
	}
	if total != 10 || success != 5 {
		t.Fatalf("expected [10,5], got [%d,%d]", total, success)
	}

	if _, _, found := GetGroupRateLimit("missing-group"); found {
		t.Fatalf("expected group 'missing-group' to not be found")
	}
}

func TestUpdateModelRequestRateLimitGroupByJSONString_InvalidJSON(t *testing.T) {
	if err := UpdateModelRequestRateLimitGroupByJSONString(`not-json`); err == nil {
		t.Fatalf("expected error for invalid JSON input")
	}
}

// TestConcurrentUpdateAndGet_NoFatalCrash is a regression test for a HIGH
// severity bug: UpdateModelRequestRateLimitGroupByJSONString used to take
// ModelRequestRateLimitMutex.RLock() (a READ lock) while replacing and
// json.Unmarshal-ing into the shared ModelRequestRateLimitGroup map (a
// WRITE). Because RLock() does not exclude other RLock() holders, a
// concurrent GetGroupRateLimit() call (also RLock()) could run at the same
// time as the writer, producing a genuine concurrent map read + map write.
// Go's runtime detects this independent of -race (it sets a "hashWriting"
// flag during map mutation and checks it on read) and aborts the whole
// process with `fatal error: concurrent map read and map write` — this is
// NOT a recoverable panic, so it takes down the entire server.
//
// This test drives enough concurrent readers/writers that, prior to the
// fix (RLock on the writer), it reliably reproduced the fatal error locally
// without needing -race/gcc. After the fix (Lock on the writer), the mutex
// fully serializes access and this is expected to be stable.
func TestConcurrentUpdateAndGet_NoFatalCrash(t *testing.T) {
	const goroutines = 64
	const iterations = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			body := `{"g` + strconv.Itoa(n) + `":[1,1]}`
			for j := 0; j < iterations; j++ {
				_ = UpdateModelRequestRateLimitGroupByJSONString(body)
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _, _ = GetGroupRateLimit("g" + strconv.Itoa(n))
			}
		}(i)
	}

	wg.Wait()
}
