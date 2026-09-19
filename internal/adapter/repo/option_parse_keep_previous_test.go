package repo

import (
	"errors"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// option_parse_keep_previous_test.go — a malformed admin value must not
// silently zero a money variable.
//
// updateOptionMap used to write every numeric option as
// `x, _ = strconv.Parse*(value)`, which on a parse failure stores the zero
// value: QuotaPerUnit 0 makes every quota-to-currency conversion in the
// console and in /api/status read 0, and ChannelDisableThreshold 0 disables
// the auto-disable threshold. The failure is reachable from one empty or
// mistyped field in Settings, and from a corrupt row on every replica at the
// next SyncOptions tick — no error is returned to the writer and nothing is
// logged, so the first symptom is a wrong number on a customer's screen.
//
// The contract: an unparseable value leaves the previous value in place and
// is reported to the caller.
func TestOptionParse_MalformedValueKeepsPreviousQuotaPerUnit(t *testing.T) {
	restoreOptionMapForTest(t)

	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	if err := SetOptionMapValue("QuotaPerUnit", "500000"); err != nil {
		t.Fatalf("SetOptionMapValue(500000): %v", err)
	}
	if common.QuotaPerUnit != 500000 {
		t.Fatalf("QuotaPerUnit = %v after a valid write, want 500000", common.QuotaPerUnit)
	}

	before := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues("QuotaPerUnit"))

	err := SetOptionMapValue("QuotaPerUnit", "abc")
	if err == nil {
		t.Errorf("SetOptionMapValue(abc) returned nil; an unparseable value must be reported")
	}
	// This is the tick path (loadOptionsFromDatabase -> updateOptionMap): its
	// message is logged on every replica on every sync, and the dispatch is
	// shared with the secret-bearing keys, so it must not quote the value.
	if err != nil && strings.Contains(err.Error(), "abc") {
		t.Errorf("the tick-path rejection quotes the submitted value: %q", err.Error())
	}
	if common.QuotaPerUnit != 500000 {
		t.Fatalf("QuotaPerUnit = %v after an unparseable write, want the previous 500000", common.QuotaPerUnit)
	}

	after := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues("QuotaPerUnit"))
	if after != before+1 {
		t.Fatalf("option_parse_rejected_total{key=QuotaPerUnit} went %v -> %v, want +1", before, after)
	}
}

// TestOptionParse_MalformedValueKeepsPreviousChannelDisableThreshold covers
// the second float option on the same dispatch, and an int one, so the fix is
// pinned on more than the single key the report named.
func TestOptionParse_MalformedValueKeepsPreviousChannelDisableThreshold(t *testing.T) {
	restoreOptionMapForTest(t)

	previousThreshold := common.ChannelDisableThreshold
	previousRetry := common.RetryTimes
	t.Cleanup(func() {
		common.ChannelDisableThreshold = previousThreshold
		common.RetryTimes = previousRetry
	})

	if err := SetOptionMapValue("ChannelDisableThreshold", "5.5"); err != nil {
		t.Fatalf("SetOptionMapValue(5.5): %v", err)
	}
	if err := SetOptionMapValue("RetryTimes", "3"); err != nil {
		t.Fatalf("SetOptionMapValue(3): %v", err)
	}

	if err := SetOptionMapValue("ChannelDisableThreshold", ""); err == nil {
		t.Errorf("an empty ChannelDisableThreshold returned nil; it must be reported")
	}
	if err := SetOptionMapValue("RetryTimes", "1e9999"); err == nil {
		t.Errorf("an out-of-range RetryTimes returned nil; it must be reported")
	}

	if common.ChannelDisableThreshold != 5.5 {
		t.Fatalf("ChannelDisableThreshold = %v, want the previous 5.5", common.ChannelDisableThreshold)
	}
	if common.RetryTimes != 3 {
		t.Fatalf("RetryTimes = %v, want the previous 3", common.RetryTimes)
	}
}

// TestOptionParse_RejectedAdminWriteDoesNotPersistTheRow — parse first, persist
// second.
//
// The other order leaves the operator with no way out inside the product: the
// options row holds a value the engine refuses, /api/option echoes that string
// back to the console, every replica keeps running the previous number, and
// every SyncOptions tick refuses the row again. The only repair is a value that
// parses — assuming somebody notices, because the console shows the string that
// was saved, not the number that is running.
func TestOptionParse_RejectedAdminWriteDoesNotPersistTheRow(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	restoreOptionMapForTest(t)

	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })

	if err := UpdateOption("QuotaPerUnit", "500000"); err != nil {
		t.Fatalf("UpdateOption(500000): %v", err)
	}
	if common.QuotaPerUnit != 500000 {
		t.Fatalf("QuotaPerUnit = %v after a valid write, want 500000", common.QuotaPerUnit)
	}

	before := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues("QuotaPerUnit"))

	err := UpdateOption("QuotaPerUnit", "abc")
	if err == nil {
		t.Fatal("UpdateOption accepted an unparseable QuotaPerUnit")
	}
	if !errors.Is(err, ErrOptionValueRejected) {
		t.Errorf("error %v does not wrap ErrOptionValueRejected, so the handler cannot tell an operator typo from a database failure", err)
	}
	// The message travels into the system log and into the HTTP response, and
	// this dispatch is shared with SMTPToken and the OAuth client secret.
	if strings.Contains(err.Error(), "abc") {
		t.Errorf("the rejection message quotes the value (%q); option values carry secrets", err.Error())
	}

	stored, found, storeErr := GetOptionValue(DB, "QuotaPerUnit")
	if storeErr != nil {
		t.Fatalf("read back the option row: %v", storeErr)
	}
	if !found {
		t.Fatal("the QuotaPerUnit row disappeared; the rejected write must leave the previous row alone, not delete it")
	}
	if stored != "500000" {
		t.Errorf("stored QuotaPerUnit = %q, want the previous 500000 — the refused value reached the options table", stored)
	}
	if common.QuotaPerUnit != 500000 {
		t.Errorf("running QuotaPerUnit = %v, want 500000", common.QuotaPerUnit)
	}

	after := testutil.ToFloat64(metrics.OptionParseRejectedTotal.WithLabelValues("QuotaPerUnit"))
	if after != before+1 {
		t.Errorf("option_parse_rejected_total{key=QuotaPerUnit} went %v -> %v, want +1", before, after)
	}
}

// TestOptionParse_RetiredKeyIsRefusedBeforeItIsPersisted covers the other class
// UpdateOption refuses: a key that is no longer a write path at all.
func TestOptionParse_RetiredKeyIsRefusedBeforeItIsPersisted(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	restoreOptionMapForTest(t)

	err := UpdateOption("group_ratio_setting.group_ratio", "{\"default\":9}")
	if err == nil {
		t.Fatal("UpdateOption accepted a retired key")
	}
	if !errors.Is(err, ErrOptionValueRejected) {
		t.Errorf("error %v does not wrap ErrOptionValueRejected", err)
	}
	if _, found, _ := GetOptionValue(DB, "group_ratio_setting.group_ratio"); found {
		t.Error("the retired key was written to the options table")
	}
}
