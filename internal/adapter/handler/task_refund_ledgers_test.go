package handler

// task_refund_ledgers_test.go — an async-task refund must hand back every
// ledger the submission debited, not just the user's balance.
//
// Submission runs app.PostConsumeQuota (relay/relay_task.go:208,
// relay/mjproxy_handler.go:223), which moves FOUR things: users.quota,
// tokens.remain_quota, the tenant credit pool, and (for wallet-bridged
// accounts) the platform wallet. Until cycle 13 the refund moved the first
// one and the two used_quota counters, so every failed MJ / Suno / video task
// permanently burned the customer's per-key allowance and the tenant's pool
// balance — money the customer never spent and cannot get back without a
// manual adjustment.
//
// The charge below is the real submission sequence (PostConsumeQuota +
// RecordConsumeLog + the two counters), not a hand-built "charged" state:
// the key/pool legs are recovered from the row the submission wrote, so a
// hand-built state would prove nothing about the path that runs in
// production.

import (
	"context"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// taskLedgerFixture is the state an async-task submission leaves behind: a
// user with a balance, a limited per-key allowance, a funded tenant pool and
// the channel the task ran on.
type taskLedgerFixture struct {
	ctx     *V2TestContext
	channel *repo.Channel
	token   *repo.Token
	pool    *repo.TenantCreditPool

	startUserQuota   int
	startTokenRemain int
	startPoolBalance int64
	startUserUsed    int
	startChannelUsed int
}

func seedTaskLedgerFixture(t *testing.T, ctx *V2TestContext, name string) *taskLedgerFixture {
	t.Helper()

	// The pool tables are not part of SetupV2TestRouter's list.
	if err := ctx.DB.AutoMigrate(&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("migrate pool tables: %v", err)
	}
	// The fixture disables consume logging; the submission row it suppresses
	// is exactly what a refund has to find, and production writes it
	// (common.LogConsumeEnabled defaults to true).
	prevLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = prevLogConsume })

	ch := SeedV2Channel(t, ctx, name+"-ch")

	token := &repo.Token{
		UserId:       ctx.NormalUser.Id,
		TenantId:     ctx.TenantID,
		Key:          common.GetRandomString(32),
		Status:       common.TokenStatusEnabled,
		Name:         name + "-key",
		CreatedTime:  common.GetTimestamp(),
		AccessedTime: common.GetTimestamp(),
		ExpiredTime:  -1,
		RemainQuota:  40_000,
		Group:        "default",
	}
	if err := ctx.DB.Create(token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	pool, err := repo.CreateTenantCreditPool(ctx.TenantID, ctx.RootUser.Id, 1_000_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, ctx.TenantID, 60_000, ctx.RootUser.Id, "seed"); err != nil {
		t.Fatalf("topup pool: %v", err)
	}

	f := &taskLedgerFixture{ctx: ctx, channel: ch, token: token, pool: pool}
	f.startUserQuota, f.startUserUsed, _ = readUserCounters(t, ctx, ctx.NormalUser.Id)
	f.startTokenRemain = f.readTokenRemain(t)
	f.startPoolBalance = f.readPoolBalance(t)
	f.startChannelUsed = readChannelUsedQuota(t, ctx, ch.Id)
	return f
}

func (f *taskLedgerFixture) readTokenRemain(t *testing.T) int {
	t.Helper()
	var tok repo.Token
	if err := f.ctx.DB.First(&tok, f.token.Id).Error; err != nil {
		t.Fatalf("read token: %v", err)
	}
	return tok.RemainQuota
}

func (f *taskLedgerFixture) readPoolBalance(t *testing.T) int64 {
	t.Helper()
	var p repo.TenantCreditPool
	if err := f.ctx.DB.First(&p, f.pool.ID).Error; err != nil {
		t.Fatalf("read pool: %v", err)
	}
	return p.CurrentBalance
}

// chargeLikeTaskSubmission reproduces relay_task.go's submission settlement
// byte for byte: the money call, then the consume row, then the two
// used_quota counters.
func (f *taskLedgerFixture) chargeLikeTaskSubmission(t *testing.T, quota int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_name", f.token.Name)
	c.Set("tenant_id", f.ctx.TenantID)

	info := &relaycommon.RelayInfo{
		UserId:          f.ctx.NormalUser.Id,
		TokenId:         f.token.Id,
		TokenKey:        f.token.Key,
		OriginModelName: "sora-2",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: f.channel.Id},
	}
	if err := app.PostConsumeQuota(info, quota, 0, true); err != nil {
		t.Fatalf("submission settlement: %v", err)
	}
	logParams := repo.RecordConsumeLogParams{
		ChannelId: f.channel.Id,
		ModelName: "sora-2",
		TokenName: f.token.Name,
		Quota:     quota,
		Content:   "操作 generate",
		TokenId:   f.token.Id,
		Group:     "default",
	}
	governance.EnrichLogParams(c, info, &logParams)
	repo.RecordConsumeLog(c, f.ctx.NormalUser.Id, logParams)
	repo.UpdateUserUsedQuotaAndRequestCount(f.ctx.NormalUser.Id, quota)
	repo.UpdateChannelUsedQuota(f.channel.Id, quota)

	// Sanity: all four ledgers really moved, so the refund assertions below
	// cannot pass against a no-op.
	if got := f.readTokenRemain(t); got != f.startTokenRemain-quota {
		t.Fatalf("submission did not debit the key: remain_quota = %d, want %d", got, f.startTokenRemain-quota)
	}
	if got := f.readPoolBalance(t); got != f.startPoolBalance-int64(quota) {
		t.Fatalf("submission did not debit the pool: balance = %d, want %d", got, f.startPoolBalance-int64(quota))
	}
}

func TestRefundTaskQuota_ReversesTokenQuotaAndPool(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	f := seedTaskLedgerFixture(t, ctx, "refund-ledgers")
	const charge = 1_300
	f.chargeLikeTaskSubmission(t, charge)

	if restored := refundTaskQuota(context.Background(), ctx.NormalUser.Id, f.channel.Id, charge,
		"Video async task failed, refund"); !restored {
		t.Fatal("refundTaskQuota reported no restore")
	}

	quota, used, _ := readUserCounters(t, ctx, ctx.NormalUser.Id)
	if quota != f.startUserQuota {
		t.Errorf("user quota = %d, want %d", quota, f.startUserQuota)
	}
	if used != f.startUserUsed {
		t.Errorf("user used_quota = %d, want %d", used, f.startUserUsed)
	}
	if got := readChannelUsedQuota(t, ctx, f.channel.Id); got != f.startChannelUsed {
		t.Errorf("channel used_quota = %d, want %d", got, f.startChannelUsed)
	}
	if got := f.readTokenRemain(t); got != f.startTokenRemain {
		t.Errorf("token remain_quota = %d, want %d — a failed task permanently burns the customer's "+
			"per-key allowance when the refund skips this ledger", got, f.startTokenRemain)
	}
	if got := f.readPoolBalance(t); got != f.startPoolBalance {
		t.Errorf("tenant pool balance = %d, want %d — the tenant keeps paying for tasks that never ran",
			got, f.startPoolBalance)
	}

	var credits []repo.TenantCreditPoolDraw
	if err := ctx.DB.Where("pool_id = ? AND direction = ?", f.pool.ID, repo.PoolDrawDirectionCredit).
		Find(&credits).Error; err != nil {
		t.Fatalf("read draws: %v", err)
	}
	found := false
	for _, d := range credits {
		if d.Amount == charge && d.Reason != repo.PoolDrawReasonTopup {
			found = true
		}
	}
	if !found {
		t.Errorf("no non-topup credit draw of %d among %d credit draw(s) — the pool balance must be "+
			"explainable from its append-only ledger", charge, len(credits))
	}
}

// TestRefundTaskQuota_AmbiguousKeySkipsKeyAndPool pins the honest half: the
// tasks table carries no token_id (entity/task.go at HEAD), so the refund
// recovers the key from the submission's consume row. When two different keys
// of the same user produced rows of the same shape, that recovery is
// ambiguous — and a guess would move money onto a key that never paid. The
// user ledger is still restored; the other two are left alone.
func TestRefundTaskQuota_AmbiguousKeySkipsKeyAndPool(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	f := seedTaskLedgerFixture(t, ctx, "refund-ambiguous")
	const charge = 900
	f.chargeLikeTaskSubmission(t, charge)

	// A second key of the same user, same channel, same cost — the shape the
	// recovery keys on, so no row identifies the payer any more.
	other := &repo.Token{
		UserId:       ctx.NormalUser.Id,
		TenantId:     ctx.TenantID,
		Key:          common.GetRandomString(32),
		Status:       common.TokenStatusEnabled,
		Name:         "refund-ambiguous-key-2",
		CreatedTime:  common.GetTimestamp(),
		AccessedTime: common.GetTimestamp(),
		ExpiredTime:  -1,
		RemainQuota:  40_000,
		Group:        "default",
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("seed second token: %v", err)
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	repo.RecordConsumeLog(c, ctx.NormalUser.Id, repo.RecordConsumeLogParams{
		ChannelId: f.channel.Id,
		ModelName: "sora-2",
		TokenName: other.Name,
		Quota:     charge,
		Content:   "操作 generate",
		TokenId:   other.Id,
		Group:     "default",
	})

	tokenBefore := f.readTokenRemain(t)
	poolBefore := f.readPoolBalance(t)

	refundTaskQuota(context.Background(), ctx.NormalUser.Id, f.channel.Id, charge, "Video async task failed, refund")

	if quota, _, _ := readUserCounters(t, ctx, ctx.NormalUser.Id); quota != f.startUserQuota {
		t.Errorf("user quota = %d, want %d — the balance refund must not depend on key recovery",
			quota, f.startUserQuota)
	}
	if got := f.readTokenRemain(t); got != tokenBefore {
		t.Errorf("token remain_quota = %d, want %d — an ambiguous recovery must not move a key's allowance",
			got, tokenBefore)
	}
	if got := f.readPoolBalance(t); got != poolBefore {
		t.Errorf("pool balance = %d, want %d — an ambiguous recovery must not credit a pool", got, poolBefore)
	}
}

// TestRefundTaskQuota_WalletLegIsCountedNotAssumed pins the honest wallet
// statement: newhub has no reverse of a settled platform debit (O-refund), so
// a refund of a wallet-bridged task makes the three local ledgers whole and
// REPORTS the wallet leg it cannot touch. A silent refund would read as "all
// four ledgers agree" on a request where the customer is still out of pocket.
func TestRefundTaskQuota_WalletLegIsCountedNotAssumed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	f := seedTaskLedgerFixture(t, ctx, "refund-wallet")
	// A wallet-bridged key: the submission's settlement moved platform money.
	if err := ctx.DB.Model(&repo.Token{}).Where("id = ?", f.token.Id).
		Update("identity_account_id", 4242).Error; err != nil {
		t.Fatalf("link token to a platform account: %v", err)
	}

	const charge = 800
	f.chargeLikeTaskSubmission(t, charge)

	before := testutil.ToFloat64(metrics.BillingTaskRefundWalletUnreversedTotal)
	refundTaskQuota(context.Background(), ctx.NormalUser.Id, f.channel.Id, charge, "task failed, refund")
	after := testutil.ToFloat64(metrics.BillingTaskRefundWalletUnreversedTotal)

	if after != before+1 {
		t.Errorf("lurus_billing_task_refund_wallet_unreversed_total = %v, want %v — an unreversible "+
			"wallet leg that nobody counts is an overcharge nobody can find", after, before+1)
	}
}
