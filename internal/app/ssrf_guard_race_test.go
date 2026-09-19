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
// still mix a filter mode from before an update with a list from after it.
// system_setting.GetFetchSettingSnapshot copies the struct under the
// configuration read lock, so the decision is taken against one published
// configuration.
func TestSSRFGuard_SnapshotReaderSeesOneCoherentConfiguration(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	previous := *fs
	t.Cleanup(func() { *system_setting.GetFetchSetting() = previous })

	fs.EnableSSRFProtection = true
	fs.DomainFilterMode = false
	fs.AllowPrivateIp = false
	fs.IpFilterMode = true
	fs.ApplyIPFilterForDomain = false

	cfg := config.GlobalConfig.Get("fetch_setting")
	if cfg == nil {
		t.Fatal("fetch_setting is not registered with the config manager")
	}

	shapes := []string{
		`["evil.invalid","pad1.invalid","pad2.invalid","pad3.invalid"]`,
		`["evil.invalid","pad4.invalid"]`,
	}
	if err := config.UpdateConfigFromMap(cfg, map[string]string{"domain_list": shapes[0]}); err != nil {
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
			if err := config.UpdateConfigFromMap(cfg, map[string]string{"domain_list": shapes[i%len(shapes)]}); err != nil {
				t.Errorf("publish domain_list: %v", err)
				return
			}
		}
	}()

	var admitted int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snapshot := system_setting.GetFetchSettingSnapshot()
			if err := common.ValidateURLWithFetchSetting(
				"http://evil.invalid",
				snapshot.EnableSSRFProtection,
				snapshot.AllowPrivateIp,
				snapshot.DomainFilterMode,
				snapshot.IpFilterMode,
				snapshot.DomainList,
				snapshot.IpList,
				nil,
				snapshot.ApplyIPFilterForDomain,
			); err == nil {
				admitted++
				return
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if admitted != 0 {
		t.Fatalf("a blacklisted host was admitted %d time(s) while the domain list was being republished", admitted)
	}
}
