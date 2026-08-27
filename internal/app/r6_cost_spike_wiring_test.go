// Copyright (c) 2026 LurusTech
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package app

import (
	"context"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestR6PostConsumeQuota_RecordsCostSpikeWindowWithoutIdentityAccount is the
// wiring lock for the cost-spike fuse's WRITE side.
//
// The fuse reads the window unconditionally (middleware/cost_spike.go) and
// admits the request when it finds nothing, so a window that is only written
// for wallet-bridged accounts means the fuse cannot trip for anyone else. On
// the live gateway that was 5 of 6 tokens, and no test noticed: every existing
// cost-spike test calls RecordCostSpikeWindow directly, so none of them
// observes whether PostConsumeQuota actually reaches it.
//
// IdentityAccountID stays 0 here deliberately — that is the whole point, and
// it mirrors TestPostConsumeQuota_RecordsBusinessTPMWindow, which pins the
// sibling TPM hook to the same "must fire outside the platform-wallet gate"
// contract.
func TestR6PostConsumeQuota_RecordsCostSpikeWindowWithoutIdentityAccount(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	withMiniRedisTPM(t)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	prevProtection := common.CostSpikeProtectionEnabled
	common.CostSpikeProtectionEnabled = true
	t.Cleanup(func() { common.CostSpikeProtectionEnabled = prevProtection })

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: key,
	}
	if err := PostConsumeQuota(relayInfo, 300, 50, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	total, err := QueryCostSpikeWindow(context.Background(), common.RDB, userId)
	if err != nil {
		t.Fatalf("QueryCostSpikeWindow: %v", err)
	}
	if total != 350 {
		t.Errorf("cost-spike window = %d, want 350 (quota 300 + preConsumed 50); "+
			"the fuse's write side is gated on IdentityAccountID and so never fires for local-quota tokens", total)
	}

	// A refund settles a negative total and must not pollute the window —
	// same contract RecordCostSpikeWindow states for its tokens<=0 guard.
	if err := PostConsumeQuota(relayInfo, -100, 0, false); err != nil {
		t.Fatalf("refund PostConsumeQuota: %v", err)
	}
	total, err = QueryCostSpikeWindow(context.Background(), common.RDB, userId)
	if err != nil {
		t.Fatalf("QueryCostSpikeWindow after refund: %v", err)
	}
	if total != 350 {
		t.Errorf("cost-spike window after refund = %d, want unchanged 350", total)
	}
}

// TestR6PostConsumeQuota_CostSpikeWindowRecordedOnceWithIdentityAccount guards
// the other direction: moving the call out of the wallet gate must not leave a
// second copy behind inside it, which would double-count every wallet-bridged
// settlement and trip the fuse at half the configured threshold.
func TestR6PostConsumeQuota_CostSpikeWindowRecordedOnceWithIdentityAccount(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	withMiniRedisTPM(t)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	prevProtection := common.CostSpikeProtectionEnabled
	common.CostSpikeProtectionEnabled = true
	t.Cleanup(func() { common.CostSpikeProtectionEnabled = prevProtection })

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 4242,
	}
	if err := PostConsumeQuota(relayInfo, 300, 50, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	total, err := QueryCostSpikeWindow(context.Background(), common.RDB, userId)
	if err != nil {
		t.Fatalf("QueryCostSpikeWindow: %v", err)
	}
	if total != 350 {
		t.Errorf("cost-spike window = %d, want exactly 350 — 700 means the settlement was recorded twice", total)
	}
}
