package handler

// cov_handler-deep-b_heartbeat_test.go — closes the remaining branches in
// user_heartbeat.go left uncovered by user_heartbeat_test.go's
// TestUserHeartbeat_StatusBranches table + the independent DisabledUser /
// TenantMismatch / MalformedBody / BearerPrefixStripped tests: the
// empty-key-after-stripping guard, the RemainQuota<=0-while-Status-Enabled
// "quota exhausted" branch (distinct from the Status=Exhausted branch the
// table already covers), the orphaned-user-row branch, the unknown-tenant-
// slug branch, and — using a genuine DB failure, never a fabricated input —
// the GetTokenByKey DB-error branch and the best-effort SelectUpdate
// write-failure branch that the handler's own doc comment says "must never
// break the EndUser session".
//
// Reuses setupHeartbeatTest / (*heartbeatTestCtx).seedToken /
// (*heartbeatTestCtx).doHeartbeat / decodeHeartbeat from
// user_heartbeat_test.go (same package).

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestUserHeartbeat_EmptyKeyAfterStripping covers the `key == ""` guard that
// fires when the Authorization header collapses to nothing after the sk-/
// Bearer stripping (e.g. a client that sends the literal "sk-" prefix with
// no key material — a real malformed-client shape, not a synthetic one).
func TestUserHeartbeat_EmptyKeyAfterStripping(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", "sk-", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Data.Status != "revoked" {
		t.Errorf("status = %q, want revoked", resp.Data.Status)
	}
}

// TestUserHeartbeat_QuotaExhaustedWhileStatusEnabled covers the
// `!token.UnlimitedQuota && token.RemainQuota <= 0` branch specifically —
// distinct from the table-driven "exhausted token" case, which flips
// token.Status to TokenStatusExhausted. Here Status stays Enabled and only
// the quota counter hits zero, matching the real relay hot-path where the
// status flip is a separate, possibly-lagging step.
func TestUserHeartbeat_QuotaExhaustedWhileStatusEnabled(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	tok := ctx.seedToken(t, repo.Token{RemainQuota: 0, UnlimitedQuota: false})
	// seedToken's override merge only applies RemainQuota when non-zero, so
	// force it directly to be sure the row really is zero.
	if err := ctx.db.Model(tok).Update("remain_quota", 0).Error; err != nil {
		t.Fatalf("force remain_quota=0: %v", err)
	}

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Data.Status != "expired" {
		t.Errorf("status = %q, want expired (quota exhausted while Status=Enabled)", resp.Data.Status)
	}
}

// TestUserHeartbeat_OrphanedUserRow covers the `userErr != nil` branch: the
// token row references a user id that no longer exists (an orphan left
// behind by a hard user delete, matching the tenant-id-drift orphan shape
// this codebase has hit before — see doc/tenant-id-drift-rootfix).
func TestUserHeartbeat_OrphanedUserRow(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	tok := ctx.seedToken(t, repo.Token{UserId: 9_999_999})

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Data.Status != "revoked" {
		t.Errorf("status = %q, want revoked (orphaned user reference)", resp.Data.Status)
	}
}

// TestUserHeartbeat_UnknownTenantSlug covers the tenant-lookup error branch
// on the multi-tenant route (a URL slug that resolves to no tenant row).
func TestUserHeartbeat_UnknownTenantSlug(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	tok := ctx.seedToken(t, repo.Token{})

	w := ctx.doHeartbeat("/api/v2/this-slug-does-not-exist/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Success {
		t.Errorf("success = true, want false for an unresolvable tenant slug")
	}
}

// TestUserHeartbeat_TokenLookupDBError forces GetTokenByKey to fail with a
// genuine (non-not-found) DB error — the tokens table is dropped from under
// a live connection — and confirms the handler distinguishes this from the
// "unknown token" 401 case by returning 500, per its own doc comment
// ("distinguish not-found (revoked) from DB error (transient, 500)").
func TestUserHeartbeat_TokenLookupDBError(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	if err := ctx.db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens table: %v", err)
	}
	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", "any-looking-key", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Success {
		t.Errorf("success = true, want false for a token-lookup DB error")
	}
}

// TestUserHeartbeat_AccessedTimeWriteFailureIsSwallowed installs a BEFORE
// UPDATE trigger that rejects every write to the tokens table (reads for
// the lookup still succeed) and confirms the handler's documented
// "single failed write must never break the EndUser session" guarantee:
// the response is still 200/active even though the accessed_time
// best-effort persist failed.
func TestUserHeartbeat_AccessedTimeWriteFailureIsSwallowed(t *testing.T) {
	ctx := setupHeartbeatTest(t)
	defer ctx.cleanup()

	tok := ctx.seedToken(t, repo.Token{})
	origAccessedTime := tok.AccessedTime

	if err := ctx.db.Exec(`CREATE TRIGGER handler_deep_b_block_hb_token_update
		BEFORE UPDATE ON tokens
		BEGIN SELECT RAISE(ABORT, 'simulated write failure'); END;`).Error; err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	w := ctx.doHeartbeat("/api/v2/"+ctx.tenant.Slug+"/user/heartbeat", tok.Key, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (write failure must be swallowed), body=%s", w.Code, w.Body.String())
	}
	resp := decodeHeartbeat(t, w)
	if resp.Data.Status != "active" || !resp.Success {
		t.Errorf("expected active/success despite the swallowed write failure, got success=%v status=%q", resp.Success, resp.Data.Status)
	}

	ctx.db.Exec(`DROP TRIGGER handler_deep_b_block_hb_token_update`)
	var reloaded repo.Token
	if err := ctx.db.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.AccessedTime != origAccessedTime {
		t.Errorf("accessed_time changed despite the write failure: %d -> %d", origAccessedTime, reloaded.AccessedTime)
	}
}
