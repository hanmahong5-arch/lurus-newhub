# Testing Guide

## Commands

```bash
go test ./...                              # all packages
go test ./internal/adapter/handler/        # specific package
go test -v ./internal/app/                 # verbose
go test -short ./...                       # unit only (skips integration via testing.Short())
go test -run Integration ./...             # integration only (needs PostgreSQL + Redis + Meilisearch)
go test -race ./...                        # race detector (run before merging to main)
go test -bench=. -benchmem ./...           # benchmarks
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

> ❌ Do NOT run individual test files (`go test ./path/file_test.go`) — fails with `undefined: repo` / `undefined: common`. A single file lacks the package's imports/deps. Always test at package level or `./...`.

### By feature

```bash
go test -v ./internal/adapter/handler/ -run Alipay
go test -v ./internal/adapter/handler/ -run TestAlipayOAuth_MissingState
go test -v ./internal/adapter/handler/ -run Release; go test -v ./internal/app/ -run Release
go test -v ./internal/app/ -run TestCompareVersions
go test -v ./internal/adapter/handler/ -run ModelSync
go test -v ./internal/adapter/handler/ -run TestAutoSyncChannelModelsWithContext_ContextCancellation
go test -bench=BenchmarkSyncAllChannelModels ./internal/adapter/handler/
```

### Frontend

```bash
cd web && bun run test           # + test:watch, lint (prettier), lint:fix, eslint
```

## Coverage targets (from CLAUDE.md)

| Layer | Target |
|-------|--------|
| `internal/app/` | ≥ 80% |
| `internal/adapter/repo/` | ≥ 60% |
| `internal/adapter/handler/` | ≥ 50% |

Current (2026-02-13): handler ~45% (14 test files, ~60 tests); app ~30% (2 files, ~15 tests); repo TBD.

## CI sequence

```bash
go test -short -cover ./...          # 1. unit + coverage
go test -race -short ./...           # 2. race
go test -run Integration ./...       # 3. integration (env: SQL_DSN)
cd web && bun run test && bun run lint && bun run eslint && bun run check:casing   # 4. frontend (no typecheck script — plain JS)
```

GitHub Actions: setup-go 1.25 → unit tests → race → integration (with services via `SQL_DSN` secret).

## Test structure

- File naming: `*_test.go`, `*_integration_test.go`, `*_benchmark_test.go`.
- Function naming: `Test<Subject>_<Method>_<Behavior>` (e.g. `TestAlipayOAuth_MissingState`). Use table-driven tests (`tests := []struct{name; ...}{...}` + `t.Run(tt.name, ...)`).

## Common issues

| Issue | Cause | Fix |
|-------|-------|-----|
| `undefined: repo` | running a single test file | test the package: `go test ./internal/adapter/handler/` |
| `database connection refused` | DB not running / wrong DSN | `go test -short ./...` to skip, or `docker-compose up -d postgres` + `export SQL_DSN=...` |
| `too many open files` (macOS) | fd limit | `ulimit -n 4096`, or `go test -p 4 ./...` |

## Best practices

Run `go test ./...` before committing · table-driven for multiple scenarios · skip slow tests with `testing.Short()` · `t.Parallel()` for independent tests · mock external deps (DB/HTTP) · test error cases · run race detector regularly. See also `CLAUDE.md#tdd`.
