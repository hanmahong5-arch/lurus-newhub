package app

import (
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// ssrf_guard_race_test.go — an SSRF decision must be taken against a list
// somebody actually published.
//
// ValidateOutboundURL reads fetch_setting once (ssrf_guard.go:56) and passes
// fs.DomainList down into common.ValidateURLWithFetchSetting, which keeps that
// slice for the whole check. Before copy-on-write, a config update decoded the
// new JSON array into the live slice: encoding/json resets the live slice's
// length to zero and appends into the same backing array, so a check already
// holding the old header kept the old length while the elements underneath
// were replaced one by one. The list it then evaluated was neither the old one
// nor the new one, and in blacklist mode — the shipped default,
// domain_filter_mode=false — a host whose entry had been overwritten was
// waved through.
//
// The window is not admin-only: repo.loadOptionsFromDatabase feeds
// fetch_setting.domain_list through the same writer on every SyncOptions tick.
//
// This test is deterministic and single-goroutine on purpose: it pins the
// property that makes the concurrent case safe (a published slice is not
// written again), rather than trying to lose a race on demand.
func TestSSRFGuard_PublishedDomainListIsNotRewrittenUnderAnInFlightCheck(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	previous := *fs
	t.Cleanup(func() { *system_setting.GetFetchSetting() = previous })

	fs.EnableSSRFProtection = true
	fs.DomainFilterMode = false // blacklist: listed hosts are rejected
	fs.AllowPrivateIp = false
	// IP whitelist mode makes ValidateOutboundURL compute
	// applyIPFilterForDomain = false, so the check stops at the domain list and
	// performs no DNS lookup — the assertions below are about the list,
	// not about the resolver.
	fs.IpFilterMode = true
	fs.ApplyIPFilterForDomain = false

	publish := func(t *testing.T, list string) {
		t.Helper()
		cfg := config.GlobalConfig.Get("fetch_setting")
		if cfg == nil {
			t.Fatal("fetch_setting is not registered with the config manager")
		}
		if err := config.UpdateConfigFromMap(cfg, map[string]string{"domain_list": list}); err != nil {
			t.Fatalf("publish domain_list %s: %v", list, err)
		}
	}

	publish(t, `["evil.invalid","pad1.invalid","pad2.invalid","pad3.invalid","pad4.invalid"]`)
	if err := ValidateOutboundURL("http://evil.invalid"); err == nil {
		t.Fatal("ValidateOutboundURL accepted a blacklisted host right after it was published")
	}

	// What a check that is already running holds at this instant.
	held := fs.DomainList

	publish(t, `["other.invalid"]`)

	if err := common.ValidateURLWithFetchSetting(
		"http://evil.invalid",
		true,  // enableSSRFProtection
		false, // allowPrivateIp
		false, // domainFilterMode: blacklist
		true,  // ipFilterMode: whitelist, so no DNS lookup below
		held,
		nil, // ipList
		nil, // allowedPorts
		false,
	); err == nil {
		t.Fatalf("an in-flight check evaluated a list nobody published (%v) and accepted evil.invalid", held)
	}
}

// TestSSRFGuard_SnapshotReaderSeesOneCoherentConfiguration covers the second
// half of the same problem: copy-on-write makes each published list immutable,
// but a caller that reads fetch_setting field by field off the live struct can
// still mix a filter mode from before an update with a list from after it —
// and a mixture of two configurations that each reject a host can admit it.
//
// The two configurations below are the whole point of the test. Each one on
// its own rejects evil.invalid:
//
//	A  blacklist mode + ["evil.invalid"]   -> listed, and listed means reject
//	B  whitelist mode + ["good.invalid"]   -> not listed, and that means reject
//
// Either mixture admits it:
//
//	mode from A + list from B -> blacklist that does not contain it -> allowed
//	mode from B + list from A -> whitelist that does contain it     -> allowed
//
// so the assertion "evil.invalid is never admitted" is exactly the assertion
// "no reader ever saw a mixture". The writer is the production one
// (ConfigManager.LoadFromDB, what repo.SyncOptions calls on every tick, which
// is where a module's keys are applied together) and the reader is the
// production one (app.ValidateOutboundURL, which every channel base_url and
// every proxy target goes through).
//
// The earlier version of this test only ever varied the domain list, and both
// of its shapes contained evil.invalid, so no mixture it could produce was
// unsafe: it passed with the lock deleted from GetFetchSettingSnapshot and
// with copy-on-write reverted. It pinned nothing.
func TestSSRFGuard_AnSSRFDecisionIsTakenAgainstOnePublishedConfiguration(t *testing.T) {
	previous := *system_setting.GetFetchSetting()
	t.Cleanup(func() { *system_setting.GetFetchSetting() = previous })

	fs := system_setting.GetFetchSetting()
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	// IP whitelist mode makes ValidateOutboundURL compute
	// applyIPFilterForDomain = false, so the check stops at the domain list and
	// performs no DNS lookup.
	fs.IpFilterMode = true
	fs.ApplyIPFilterForDomain = false

	shapes := []map[string]string{
		{ // A: blacklist containing the host
			"fetch_setting.domain_filter_mode": "false",
			"fetch_setting.domain_list":        `["evil.invalid"]`,
		},
		{ // B: whitelist not containing the host
			"fetch_setting.domain_filter_mode": "true",
			"fetch_setting.domain_list":        `["good.invalid"]`,
		},
	}
	for i, shape := range shapes {
		if err := config.GlobalConfig.LoadFromDB(shape); err != nil {
			t.Fatalf("seed publish %d: %v", i, err)
		}
		if err := ValidateOutboundURL("http://evil.invalid"); err == nil {
			t.Fatalf("shape %d admits evil.invalid on its own; the test would prove nothing", i)
		}
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
			if err := config.GlobalConfig.LoadFromDB(shapes[i%len(shapes)]); err != nil {
				t.Errorf("publish fetch_setting: %v", err)
				return
			}
		}
	}()

	var admitted int
	var admittedWith system_setting.FetchSetting
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := ValidateOutboundURL("http://evil.invalid"); err == nil {
				admittedWith = system_setting.GetFetchSettingSnapshot()
				admitted++
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if admitted != 0 {
		t.Fatalf("evil.invalid was admitted %d time(s) while fetch_setting was being republished; a reader mixed a filter mode from one publication with a list from another (nearest snapshot: whitelist=%v list=%v)",
			admitted, admittedWith.DomainFilterMode, admittedWith.DomainList)
	}
}
