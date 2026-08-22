#!/usr/bin/env bash
# check-entverify-vendor.sh — consistency gate for the vendored pkg/entverify
# copy at internal/pkg/entverify/. Source of truth: platform's pkg/entverify
# (github.com/hanmahong5-arch/lurus-platform).
#
# Checks:
#   1. entverify.go — sha256, vendor header (first 4 lines) skipped, compared
#      against platform's file. Zero adaptation by design (see the file's own
#      vendor header) so this MUST match byte-for-byte past the header.
#   2. entverify_test.go — full diff against platform's file. Also zero
#      adaptation (see its header comment: "self-contained... copied wholesale
#      into a consumer repo with zero platform coupling").
#   3. entitlement_conformance_test.go — sha256, vendor header skipped,
#      against a pinned expected value. This file IS adapted from platform's
#      original: Go's internal-package boundary forbids importing platform's
#      internal/pkg/entsign from this module, so the SIGNER KEY FLOOR check is
#      reimplemented locally (see the file's own vendor header for specifics).
#      A live diff against platform would therefore never be clean by design,
#      so this instead pins the adapted content and catches an unauthorized
#      local hand-edit of it.
#
# Platform sibling resolution: ../2l-svc-platform (local dev layout, both
# repos checked out side by side) or ../shared/lurus-platform (the layout
# go-ci.yml's "Relocate sibling repos to ../shared" step uses for the OTHER
# siblings it checks out — platform itself is not currently one of them, so
# this path is here for forward compatibility). When neither is found, check
# 1 falls back to a hardcoded expected sha256 (kept in sync via platform's
# core.yaml "Entitlement-token conformance gate" job-summary line); check 2
# is skipped with a warning (nothing to diff against); check 3 needs no
# platform workspace at all (it only ever compares against the pinned value).
#
# Exit 0 = consistent. Exit 1 = drifted.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

VENDOR_DIR="internal/pkg/entverify"
FAIL=0

PLATFORM_DIR=""
for candidate in "../2l-svc-platform" "../shared/lurus-platform"; do
  if [ -f "${candidate}/pkg/entverify/entverify.go" ]; then
    PLATFORM_DIR="${candidate}"
    break
  fi
done

guidance() {
  echo "" >&2
  echo "  -> re-copy from platform: pkg/entverify/{entverify.go,entverify_test.go} verbatim" >&2
  echo "     (keep the 4-line vendor header); for entitlement_conformance_test.go, re-copy" >&2
  echo "     then re-apply the import adjustment documented in its vendor header." >&2
  echo "     See each file's own vendor header in ${VENDOR_DIR}/ for the exact source." >&2
}

# --- check 1: entverify.go ---------------------------------------------------
# Fallback pinned 2026-08-22 from platform pkg/entverify/entverify.go (commit
# bc3d32af — see entverify.go's vendor header). Re-derive with
# `sha256sum pkg/entverify/entverify.go` on platform's side (also printed by
# core.yaml's "Entitlement-token conformance gate" job summary) and update
# this constant whenever the vendored copy is re-copied.
FALLBACK_ENTVERIFY_SHA256="f940607e36d30597085bfae75a83cf5a0b7d925b46fc868ca85061dcce2abe76"

entverify_local_sha=$(tail -n +5 "${VENDOR_DIR}/entverify.go" | sha256sum | cut -d' ' -f1)

if [ -n "${PLATFORM_DIR}" ]; then
  entverify_platform_sha=$(sha256sum "${PLATFORM_DIR}/pkg/entverify/entverify.go" | cut -d' ' -f1)
  if [ "${entverify_local_sha}" != "${entverify_platform_sha}" ]; then
    echo "FAIL: entverify.go drifted from ${PLATFORM_DIR}/pkg/entverify/entverify.go (sha256 ${entverify_local_sha} != ${entverify_platform_sha})" >&2
    FAIL=1
  else
    echo "OK: entverify.go matches ${PLATFORM_DIR}/pkg/entverify/entverify.go"
  fi
else
  echo "NOTE: platform workspace not found (checked ../2l-svc-platform, ../shared/lurus-platform); falling back to the pinned sha256" >&2
  if [ "${entverify_local_sha}" != "${FALLBACK_ENTVERIFY_SHA256}" ]; then
    echo "FAIL: entverify.go sha256 ${entverify_local_sha} != pinned fallback ${FALLBACK_ENTVERIFY_SHA256}" >&2
    FAIL=1
  else
    echo "OK: entverify.go matches the pinned fallback sha256"
  fi
fi

# --- check 2: entverify_test.go ----------------------------------------------
if [ -n "${PLATFORM_DIR}" ]; then
  if diff -q "${VENDOR_DIR}/entverify_test.go" "${PLATFORM_DIR}/pkg/entverify/entverify_test.go" >/dev/null; then
    echo "OK: entverify_test.go matches ${PLATFORM_DIR}/pkg/entverify/entverify_test.go"
  else
    echo "FAIL: entverify_test.go drifted from ${PLATFORM_DIR}/pkg/entverify/entverify_test.go" >&2
    FAIL=1
  fi
else
  echo "NOTE: platform workspace not found; skipping entverify_test.go diff" >&2
fi

# --- check 3: entitlement_conformance_test.go ---------------------------------
# Pinned 2026-08-22 — sha256 of THIS repo's adapted file (header skipped), not
# platform's original (see the file's own vendor header for why a live diff
# against platform is not meaningful here). Update after any deliberate
# re-vendor + re-adaptation.
FALLBACK_CONFORMANCE_SHA256="6d3cc1b07463e9bf28cc7b67253b0ee8012c2f6ab6d3dc72da5a3fa0b5b6bee3"

conformance_local_sha=$(tail -n +5 "${VENDOR_DIR}/entitlement_conformance_test.go" | sha256sum | cut -d' ' -f1)
if [ "${conformance_local_sha}" != "${FALLBACK_CONFORMANCE_SHA256}" ]; then
  echo "FAIL: entitlement_conformance_test.go sha256 ${conformance_local_sha} != pinned ${FALLBACK_CONFORMANCE_SHA256}" >&2
  FAIL=1
else
  echo "OK: entitlement_conformance_test.go matches the pinned sha256"
fi

if [ "${FAIL}" -ne 0 ]; then
  guidance
  exit 1
fi

echo "check-entverify-vendor: all vendored entverify artifacts consistent."
