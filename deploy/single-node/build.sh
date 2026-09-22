#!/usr/bin/env bash
# Build lurus-hub:local from local source.
#
# The Dockerfile's `COPY lurus-proto-go/ /shared/lurus-proto-go/` requires the
# proto-go repo to live inside the build context, but its canonical location
# is the sibling path ../shared/lurus-proto-go (matching go.mod's `replace`
# directive). This script stages it into the build context for the duration
# of the docker build, then removes it. The directory is in .gitignore so the
# transient copy never ends up in a commit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HUB_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PROTO_SRC="$(cd "${HUB_ROOT}/.." && pwd)/shared/lurus-proto-go"
PROTO_DST="${HUB_ROOT}/lurus-proto-go"
STAMP="${PROTO_DST}/.staged-by-build-script"

cleanup() {
  if [[ -f "${STAMP}" ]]; then
    rm -rf "${PROTO_DST}" "${HUB_ROOT}/lurus-entkit"
  fi
}
trap cleanup EXIT INT TERM

if [[ ! -d "${PROTO_SRC}" ]]; then
  echo "ERROR: lurus-proto-go not found at ${PROTO_SRC}" >&2
  echo "       Make sure the sibling repo is checked out alongside lurus-hub." >&2
  exit 1
fi

if [[ -d "${PROTO_DST}" && ! -f "${STAMP}" ]]; then
  echo "ERROR: ${PROTO_DST} already exists and isn't owned by this script." >&2
  echo "       Refusing to overwrite. Move/remove it manually first." >&2
  exit 1
fi

echo "==> Staging lurus-proto-go into build context"
rm -rf "${PROTO_DST}"
if command -v rsync >/dev/null 2>&1; then
  rsync -a --exclude='.git' "${PROTO_SRC}/" "${PROTO_DST}/"
else
  cp -r "${PROTO_SRC}" "${PROTO_DST}"
fi
touch "${STAMP}"

# The entitlement kit (Dockerfile: COPY lurus-entkit/) is staged the same way.
KIT_SRC="$(cd "${HUB_ROOT}/.." && pwd)/shared/lurus-entkit"
KIT_DST="${HUB_ROOT}/lurus-entkit"
if [[ ! -d "${KIT_SRC}" ]]; then
  echo "ERROR: lurus-entkit not found at ${KIT_SRC}" >&2
  exit 1
fi
rm -rf "${KIT_DST}"
cp -r "${KIT_SRC}" "${KIT_DST}"
rm -rf "${KIT_DST}/.git"

VERSION="$(cat "${HUB_ROOT}/VERSION" 2>/dev/null)"
[[ -z "${VERSION}" ]] && VERSION="dev"

echo "==> docker build (this can take 5-10 min on first run)"
cd "${HUB_ROOT}"
docker build \
  --tag "lurus-hub:local" \
  --tag "lurus-hub:${VERSION}" \
  .

echo
echo "==> Built: lurus-hub:local  (also tagged lurus-hub:${VERSION})"
echo "    Next: cd ${SCRIPT_DIR} && docker compose up -d"
