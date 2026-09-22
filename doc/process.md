# Development Progress / 开发进度

> Last Updated: 2026-09-22
> **Rule**: 每条目 ≤ 15 行（HARD LIMIT），只记录已完成工作的极简摘要。
>
> **2026-09-22 瘦身**: 2026-02-04 → 2026-05-18 的逐条进度日志（Epic 1–11、
> Story 6-x/7-x/8-x/9-x、hardening / reseller-MVP / Q3 swarm 各轮）已删除。
> 那段记录的每一条都对应一个已合并的 PR，权威版本在 `git log` 里，而文档副本
> 引用的一批 story/acceptance 文档同轮一并删除（见下）。这里只保留仍会被引用
> 的两条，之后的每一轮走 PR 正文 + `_bmad-output/planning-artifacts/` 的当轮
> 计划文档，不再往这里追加。

---

## 2026-06-03 · CI: pg-integration disposable-PG gate (真 PG / 钱路 e2e)

承接 race 闸门 re-block (#11)，闭合审计 doc 里 "真 PG / 钱路 e2e" 这条 deferred followup。

- **病灶**: `internal/adapter/repo` 的 ~11 个集成测试经 `SetupTestDB` 取 PG，无 `TEST_POSTGRES_DSN` 即 `t.Skip` → CI 里从不跑、报空绿（§4.1③ hollow skip）。覆盖钱路不变量（quota 增减原子性、token validate/expire/exhaust、tenant-whitelist auth、daily-reset 幂等）。
- **修复 (PR #12 `887724bb`, 2 commits)**: go-ci.yml 加 `pg-integration` job — 起 disposable `postgres:16` service、设 DSN、跑 `./internal/adapter/repo/...`。反-hollow 卫士: `TestIntegrationPGHarness_RealPostgres` 哨兵用 `SELECT version()` 证活 PG，grep step 在哨兵被 skip/缺席时 fail job。新增 `TestIntegrationUserQuota_ConcurrentDebit_NoLostUpdate`（仅真 PG 有意义: 50 并发 `DecreaseUserQuota` → quota 恰好落 0）。BLOCKING，非 report-only。
- **证据（本地实跑，Docker v28.2.2 可用 → 不像 -race 只能靠 CI）**: `docker run postgres:16-alpine` + DSN → **719 PASS / 0 FAIL / 7 SKIP**（7 = `-short` stress + SQLite-only），0 个 "DSN not set" skip。CI 复现绿（PostgreSQL 16.14，哨兵 + 并发 e2e 均 PASS）。merge 后 main CI 全 8 job 绿。
- **范围**: off `origin/main`，纯增量（CI job + 2 测试文件），**零 money-path 源改动**。
- **未做（followup）**: PG-only 路径跑 `-race`（这些路径从未经探测器，可能暴露既存 race，单独硬化）。

## 2026-09-07 · METRICS-HONESTY: dead series + phantom dashboards retired

`channel_health`/`channel_consecutive_errors`/`channel_errors_total`（连同
`RecordChannelError`/`SetChannelHealth`/`ResetChannelErrors`）从
`internal/pkg/metrics/metrics.go` 删除 — grep 全仓零非测试调用方，三条自
`metrics.go` 建文件（2026-02-05，即约 7 个月）起从未写入的指标。`deploy/grafana/`（3 个从未被任何 kustomization apply 的
dashboard/alert 文件）整目录删除；`doc/decisions/observability.md` 补
2026-09-07 更新块订正 Jaeger "staging: deployed" 的过期声明（OTLP collector
已停,监控栈=Netdata 自托管）。`billing_debit_amount_cny` 标签从 `tenant_id`
改 `product,op`，补上 `quota.go` PostConsumeQuota 结算分支（此前只有直接
`DebitWalletGRPC` 调用点写这个指标,pre-auth settle 分支动了真钱却零观测）与
HTTP fallback `DebitWallet` 的写入点。`relay_requests_total`/
`relay_errors_total`/`relay_total_duration` 加 `product` 标签,`status` 新增
`client_gone`（`handler.relayOutcome`,调用方断连不再算 "success"）。新增
`internal/pkg/metrics/declared_series_written_test.go` 闸门：每个
`promauto.New*` 变量必须有真实生产写入方，否则 CI 测试失败。
