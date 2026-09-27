package repo

// token_validate_sentinel_test.go — cycle 18 lane L3: ValidateUserToken's
// non-quota failures must be matchable with errors.Is, the same way
// ErrTokenQuotaExhausted/ErrTokenDisabled already are. middleware.TokenAuth
// maps them onto distinct wire states (X-Lurus-Token-State) so the newapi
// bridge can tell "hub never heard of this key" apart from "hub revoked it"
// and from "hub's DB is down" — three verdicts that used to share one 401.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestValidateUserToken_StatusExpired_IsErrTokenExpired(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "sentinel_exp_status", "sentinelexpstatus@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExpired, true, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("errors.Is(err, ErrTokenExpired) = false, want true; err=%v", err)
	}
}

func TestValidateUserToken_ExpiredByTime_IsErrTokenExpired(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "sentinel_exp_time", "sentinelexptime@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, common.GetTimestamp()-1)

	_, err := ValidateUserToken(tok.Key)
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("errors.Is(err, ErrTokenExpired) = false, want true; err=%v", err)
	}
}

// A closed connection is the cheapest reproducible "DB.First failed for a
// reason other than NotFound"; the sentinel must wrap it and the wrapped
// text must not be mistaken for the NotFound "invalid token" verdict.
func TestValidateUserToken_LookupFailure_IsErrTokenLookupFailed(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "sentinel_lookup", "sentinellookup@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	sqlDB, err := DB.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	_, err = ValidateUserToken(tok.Key)
	if !errors.Is(err, ErrTokenLookupFailed) {
		t.Errorf("errors.Is(err, ErrTokenLookupFailed) = false, want true; err=%v", err)
	}
}

func TestValidateUserToken_NotFound_IsNeitherSentinel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := ValidateUserToken(common.GetRandomString(48))
	if err == nil {
		t.Fatal("expected error for an unknown key")
	}
	if errors.Is(err, ErrTokenLookupFailed) || errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrTokenDisabled) {
		t.Errorf("unknown key must not match any state sentinel; err=%v", err)
	}
}
