package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// TestConfigLockCallersAreTheKnownSet — the reentrancy gate.
//
// config.RLock is a sync.RWMutex read lock, and sync.RWMutex is not reentrant:
// if an accessor that holds it calls a second accessor that takes it, and a
// writer (the option-sync tick) arrives in between, the second RLock queues
// behind the writer and the writer queues behind the first RLock. That is a
// deadlock of a relay request, not a slow path, and it would appear in
// production at a rate proportional to SYNC_FREQUENCY.
//
// Nothing in the type system stops a future accessor from nesting, so this
// gate pins WHO takes the lock. Adding an accessor is fine — add it to the
// list below, after checking that it does not call another entry on the list
// while holding the lock.
//
// Scope, stated so it is not read as more: it matches calls written as
// `config.RLock()` / `config.Lock()` (and the Unlock halves) in non-test .go
// files under internal/, which is how every caller outside this package must
// spell it. Calls on fieldMu from inside this package are not matched; this
// package's own uses are configToMap and applyConfigMap, both of which take
// the lock and call nothing while holding it.
func TestConfigLockCallersAreTheKnownSet(t *testing.T) {
	root := configGateRepoRoot(t)

	// file -> function -> the locks it takes, with the reason it is safe.
	expected := map[string]string{
		// Read-only accessors: each reads its own package's registered struct
		// and returns. None of them calls another accessor.
		"internal/pkg/setting/model_setting/gemini.go:GetGeminiSafetySetting":          "RLock",
		"internal/pkg/setting/model_setting/gemini.go:GetGeminiVersionSetting":         "RLock",
		"internal/pkg/setting/model_setting/gemini.go:IsGeminiModelSupportImagine":     "RLock",
		"internal/pkg/setting/model_setting/global.go:ShouldPreserveThinkingSuffix":    "RLock",
		"internal/pkg/setting/model_setting/claude.go:WriteHeaders":                    "RLock",
		"internal/pkg/setting/model_setting/claude.go:GetDefaultMaxTokens":             "RLock",
		"internal/pkg/setting/system_setting/fetch_setting.go:GetFetchSettingSnapshot": "RLock",

		// Check under the read lock, release, repair under the write lock,
		// re-checking. The write half is reached only when the read half found
		// the value missing, so the two are never held together.
		"internal/pkg/setting/model_setting/claude.go:GetClaudeSettings":                  "RLock",
		"internal/pkg/setting/model_setting/claude.go:republishClaudeDefaultMaxTokens":    "Lock",
		"internal/pkg/setting/ratio_setting/group_ratio.go:GetGroupRatioSetting":          "RLock",
		"internal/pkg/setting/ratio_setting/group_ratio.go:repairGroupSpecialUsableGroup": "Lock",
		"internal/pkg/setting/operation_setting/monitor_setting.go:applyMonitorOverride":  "RLock+Lock",
	}

	found := map[string]map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// Another lane mid-edit must not be reported as a finding here.
			t.Logf("skipping unparseable %s: %v", rel, parseErr)
			return nil
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "config" || pkg.Obj != nil {
					return true
				}
				if sel.Sel.Name != "RLock" && sel.Sel.Name != "Lock" {
					return true
				}
				site := filepath.ToSlash(rel) + ":" + fn.Name.Name
				if found[site] == nil {
					found[site] = map[string]bool{}
				}
				found[site][sel.Sel.Name] = true
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	var problems []string
	for site, locks := range found {
		taken := "RLock"
		switch {
		case locks["RLock"] && locks["Lock"]:
			taken = "RLock+Lock"
		case locks["Lock"]:
			taken = "Lock"
		}
		want, known := expected[site]
		if !known {
			problems = append(problems, fmt.Sprintf(
				"%s takes the configuration %s and is not in the known set — check that it does not call another locked accessor while holding it (sync.RWMutex is not reentrant), then add it to TestConfigLockCallersAreTheKnownSet",
				site, taken))
			continue
		}
		if want != taken {
			problems = append(problems, fmt.Sprintf("%s takes %s, the known set says %s", site, taken, want))
		}
	}
	for site := range expected {
		if found[site] == nil {
			problems = append(problems, fmt.Sprintf("%s no longer takes the configuration lock; remove it from the known set", site))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("configuration lock call sites changed:\n  %s", strings.Join(problems, "\n  "))
	}
	t.Logf("%d configuration lock call sites, all known", len(found))
}

// configGateRepoRoot walks up from this package directory to the module root.
func configGateRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root above this package")
	return ""
}
