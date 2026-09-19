package config

import (
	"sync"
	"testing"
	"time"
)

// config_cow_test.go — the copy-on-write contract for hot-reloaded config.
//
// Every registered config struct (gemini, claude, global, fetch_setting,
// group_ratio_setting, console_setting, checkin/general/monitor/quota_setting,
// discord, legal, oidc — the 13 Register call sites found by
// `grep -rn "GlobalConfig.Register" --include=*.go .`) is a live object that
// relay goroutines read while the option-sync tick (repo.SyncOptions, every
// SYNC_FREQUENCY seconds) writes it. Before this file, the reflect writer
// handed the field's own address to encoding/json, which for a map keeps the
// existing entries and writes new ones INTO the live map, and for a slice
// resets the live slice's length to zero and re-appends into the live backing
// array. Two consequences, both reachable from one admin edit:
//
//   - a reader ranging over the map while the tick writes it takes the
//     runtime's "concurrent map read and map write" fatal error, which is not
//     recoverable and kills the process (all three replicas, on the same tick);
//   - a reader that already grabbed the slice header — the SSRF domain list is
//     read exactly this way, app/ssrf_guard.go passes fs.DomainList down into
//     common.ValidateURLWithFetchSetting — keeps its old length while the
//     elements underneath change, so the list it evaluates is a mixture that
//     nobody ever published.
//
// The fix is copy-on-write: decode into a freshly allocated value and publish
// it with one Set under fieldMu, so a published map/slice is not written
// again. These two tests pin the "not written again" half; the concurrency
// half is pinned by TestConfigCOW_ConcurrentReaderNeverSeesAPartialMap below
// and, on the production accessor path, by
// internal/adapter/repo/option_race_test.go.

type cowSample struct {
	Meta map[string]string `json:"meta"`
	Tags []string          `json:"tags"`
}

func TestConfigCOW_MapUpdateLeavesThePublishedMapAlone(t *testing.T) {
	s := &cowSample{Meta: map[string]string{"a": "1"}}
	held := s.Meta // what a reader that read the field one instruction ago holds

	if err := UpdateConfigFromMap(s, map[string]string{"meta": `{"b":"2"}`}); err != nil {
		t.Fatalf("UpdateConfigFromMap: %v", err)
	}

	if len(held) != 1 || held["a"] != "1" {
		t.Fatalf("the map a reader already held was mutated in place: got %v, want map[a:1]", held)
	}
	if len(s.Meta) != 1 || s.Meta["b"] != "2" {
		t.Fatalf("published map = %v, want exactly map[b:2] (the update replaces, it does not merge)", s.Meta)
	}
}

func TestConfigCOW_SliceUpdateLeavesThePublishedSliceAlone(t *testing.T) {
	s := &cowSample{Tags: []string{"x", "y", "z"}}
	held := s.Tags

	if err := UpdateConfigFromMap(s, map[string]string{"tags": `["p"]`}); err != nil {
		t.Fatalf("UpdateConfigFromMap: %v", err)
	}

	if len(held) != 3 || held[0] != "x" || held[1] != "y" || held[2] != "z" {
		t.Fatalf("the slice a reader already held was mutated in place: got %v, want [x y z]", held)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "p" {
		t.Fatalf("published slice = %v, want [p]", s.Tags)
	}
}

// TestConfigCOW_ConcurrentReaderNeverSeesAPartialMap is the in-package
// equivalent of what internal/adapter/repo/option_race_test.go asserts through
// the real gemini accessor: a reader holding RLock while a writer republishes
// sees one whole shape rather than a half-decoded one, and the process does not
// take the runtime's fatal concurrent-map error.
func TestConfigCOW_ConcurrentReaderNeverSeesAPartialMap(t *testing.T) {
	s := &cowSample{Meta: map[string]string{"k": "a"}}

	shapes := []string{
		`{"k":"a","p1":"a","p2":"a","p3":"a","p4":"a"}`,
		`{"k":"b","p5":"b","p6":"b"}`,
	}
	// Publish one of the two shapes before the reader starts, so the only
	// states it can legitimately observe are the two the writer alternates.
	if err := UpdateConfigFromMap(s, map[string]string{"meta": shapes[0]}); err != nil {
		t.Fatalf("seed publish: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := UpdateConfigFromMap(s, map[string]string{"meta": shapes[i%len(shapes)]}); err != nil {
				t.Errorf("UpdateConfigFromMap: %v", err)
				return
			}
		}
	}()

	var bad int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			RLock()
			size := len(s.Meta)
			value := s.Meta["k"]
			RUnlock()
			if (size != 5 || value != "a") && (size != 3 || value != "b") {
				bad++
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if bad != 0 {
		t.Fatalf("a reader observed a shape nobody published %d time(s)", bad)
	}
}
