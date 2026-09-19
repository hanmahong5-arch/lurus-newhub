package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// option_reject_test.go — what PUT /api/option does with a value the engine
// cannot apply.
//
// Three things have to be true at once, and before this they were not:
//
//  1. The options row is NOT written. Writing it first and refusing the value
//     afterwards leaves the row and the running configuration disagreeing
//     forever: the console reads the row back, the engine keeps the old number,
//     and every SyncOptions tick refuses the row again on every replica.
//  2. The answer is a 4xx. The v1 body shape ({success:false, message}) is
//     unchanged because switch consumes v1, but the status code stops claiming
//     the request was fine.
//  3. There is an audit row. An authenticated admin write that changed nothing
//     is still an admin write; leaving no trace of the ones that failed means
//     audit_events answers "who set this" incorrectly by omission.

func TestUpdateOption_UnparseableValueIs400NotPersistedAndAudited(t *testing.T) {
	setupPricingWriteRouter(t)

	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	// Seed through the repository rather than the handler, so the only
	// option.updated audit row in this database is the one under test.
	if err := repo.UpdateOption("QuotaPerUnit", "500000"); err != nil {
		t.Fatalf("seed QuotaPerUnit: %v", err)
	}

	w := callLegacyRootHandler(t, http.MethodPut, "/api/option/",
		map[string]interface{}{"key": "QuotaPerUnit", "value": "abc"}, UpdateOption)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a value that cannot be applied; body: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	if body["success"] != false {
		t.Errorf("success = %v, want false (the v1 body shape must not change)", body["success"])
	}
	message, _ := body["message"].(string)
	if message == "" {
		t.Error("message is empty; the operator has to be told which key was refused")
	}
	if !strings.Contains(message, "QuotaPerUnit") {
		t.Errorf("message %q does not name the key", message)
	}
	// Option values include SMTPToken, GitHubClientSecret and
	// TurnstileSecretKey, and this is the shared response path for all of them.
	if strings.Contains(message, "abc") {
		t.Errorf("message %q quotes the submitted value", message)
	}

	stored, found, err := repo.GetOptionValue(repo.DB, "QuotaPerUnit")
	if err != nil {
		t.Fatalf("read back the option row: %v", err)
	}
	if !found || stored != "500000" {
		t.Errorf("stored QuotaPerUnit = %q (found=%v), want the previous 500000 — the refused value reached the options table", stored, found)
	}
	if common.QuotaPerUnit != 500000 {
		t.Errorf("running QuotaPerUnit = %v, want 500000", common.QuotaPerUnit)
	}

	event := pollAuditRow(t, governance.ActionOptionUpdated, 2*time.Second)
	if event == nil {
		t.Fatal("no option.updated audit row for the refused admin write")
	}
	var details struct {
		Key     string `json:"key"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(event.Details), &details); err != nil {
		t.Fatalf("unmarshal audit details: %v — raw: %s", err, event.Details)
	}
	if details.Key != "QuotaPerUnit" || details.Outcome != "rejected" {
		t.Errorf("audit details = %+v, want key QuotaPerUnit / outcome rejected", details)
	}
	if strings.Contains(event.Details, "abc") {
		t.Errorf("the audit row carries the submitted value: %s", event.Details)
	}
}

// TestUpdateOption_ValidValueStillPersistsAndAudits is the other half: the
// refusal above must not have been bought by refusing everything.
func TestUpdateOption_ValidValueStillPersistsAndAudits(t *testing.T) {
	setupPricingWriteRouter(t)

	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	w := callLegacyRootHandler(t, http.MethodPut, "/api/option/",
		map[string]interface{}{"key": "QuotaPerUnit", "value": "250000"}, UpdateOption)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	if body["success"] != true {
		t.Fatalf("success = %v, want true; body: %s", body["success"], w.Body.String())
	}
	if common.QuotaPerUnit != 250000 {
		t.Errorf("running QuotaPerUnit = %v, want 250000", common.QuotaPerUnit)
	}
	stored, found, err := repo.GetOptionValue(repo.DB, "QuotaPerUnit")
	if err != nil {
		t.Fatalf("read back the option row: %v", err)
	}
	if !found || stored != "250000" {
		t.Errorf("stored QuotaPerUnit = %q (found=%v), want 250000", stored, found)
	}

	event := pollAuditRow(t, governance.ActionOptionUpdated, 2*time.Second)
	if event == nil {
		t.Fatal("no option.updated audit row for the accepted admin write")
	}
	if !strings.Contains(event.Details, "QuotaPerUnit") {
		t.Errorf("audit details = %s, want the key", event.Details)
	}
	if strings.Contains(event.Details, "rejected") {
		t.Errorf("audit details = %s, want no rejection marker on an accepted write", event.Details)
	}
}

// TestGetOptions_TakesTheConfigurationReadLockNotTheWriteLock pins the
// Lock -> RLock change on the console's options listing.
//
// GetOptions only ranges over common.OptionMap. Taking the write lock for that
// serialised every concurrent console load against every other one and against
// the SyncOptions tick, which holds the same mutex while it applies each row.
// The observable difference is exactly this: with a reader holding the lock,
// a read-locking GetOptions runs and a write-locking one blocks.
func TestGetOptions_TakesTheConfigurationReadLockNotTheWriteLock(t *testing.T) {
	gin.SetMode(gin.TestMode)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	// Hold the lock for reading, the way a concurrent console load or the
	// option-sync tick's own read would.
	common.OptionMapRWMutex.RLock()
	held := true
	releaseOnce := func() {
		if held {
			common.OptionMapRWMutex.RUnlock()
			held = false
		}
	}
	defer releaseOnce()

	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
		GetOptions(c)
		done <- w.Code
	}()

	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("GetOptions returned %d, want 200", code)
		}
	case <-time.After(2 * time.Second):
		// Let the blocked goroutine finish rather than leaking it into the
		// rest of the package's tests.
		releaseOnce()
		<-done
		t.Fatal("GetOptions did not return while another goroutine held the option map for reading; it is taking the write lock for a read-only iteration")
	}
}
