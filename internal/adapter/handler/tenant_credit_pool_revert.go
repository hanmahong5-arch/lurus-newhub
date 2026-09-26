package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// revertPoolTopupDebit gives back a wallet debit whose pool credit did not
// land, and reports whether the money is back.
//
// The revert carries its own key (idemKey + ":revert") so the platform never
// dedupes it against the debit — and so two reverts of one debit collapse into
// one. When the revert itself fails the debit is STRANDED: it is persisted as
// an open fund event under strandedEventID, which the background sweep
// (app.ReconcileStrandedTopups) compensates by crediting the pool. The log
// line is context for operators, not the only trace.
//
// strandedEventID is the caller's key on the ordinary failure path. When the
// caller's (tenant, key) slot is already held by another row it must be a
// derived id (poolTopupCollisionEventID): app.RecordStrandedTopup reads a
// unique violation as "already recorded" and would drop the debit silently.
func revertPoolTopupDebit(c *gin.Context, accountID int64, walletAmount float64,
	tenantID string, poolID, amount int64, idemKey, strandedEventID, cause string) bool {
	rerr := creditWallet(
		c.Request.Context(), accountID, walletAmount,
		"pool_topup_revert",
		"Revert: pool topup failed for tenant "+tenantID,
		"newhub", idemKey+":revert",
	)
	if rerr == nil {
		return true
	}
	if serr := app.RecordStrandedTopup(c.Request.Context(), strandedEventID, tenantID, poolID, amount); serr != nil {
		common.SysError("failed to persist stranded topup event (log is the only trace!) " +
			"event_id=" + strandedEventID + " err=" + serr.Error())
	}
	common.SysError("STRANDED wallet debit — pool topup AND revert both failed. " +
		"event_id=" + strandedEventID +
		" idempotency_key=" + idemKey +
		" account=" + strconv.FormatInt(accountID, 10) +
		" tenant=" + tenantID +
		" amount=" + strconv.FormatInt(amount, 10) +
		" cause=" + cause + " revert_err=" + rerr.Error())
	return false
}
