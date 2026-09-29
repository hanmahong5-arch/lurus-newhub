#!/usr/bin/env bash
# Build lurus-hub:local from local source.
#
# The Dockerfile's `COPY lurus-entkit/ /shared/lurus-entkit/` requires the
# entitlement kit to live inside the build context, but its canonical location
# is the sibling path ../shared/lurus-entkit (matching go.mod's `replace`
# directive). This script stages it into the build context for the duration
# of the docker build, then removes it. The directory is in .gitignore so the
# transient copy never ends up in a commit.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HUB_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
KIT_SRC="$(cd "${HUB_ROOT}/.." && pwd)/shared/lurus-entkit"
KIT_DST="${HUB_ROOT}/lurus-entkit"
STAMP="${KIT_DST}/.staged-by-build-script"

cleanup() {
  if [[ -f "${STAMP}" ]]; then
    rm -rf "${KIT_DST}"
  fi
}
trap cleanup EXIT INT TERM

if [[ ! -d "${KIT_SRC}" ]]; then
  echo "ERROR: lurus-entkit not found at ${KIT_SRC}" >&2
  echo "       Make sure the sibling repo is checked out alongside lurus-hub." >&2
  exit 1
fi

if [[ -d "${KIT_DST}" && ! -f "${STAMP}" ]]; then
  echo "ERROR: ${KIT_DST} already exists and isn't owned by this script." >&2
  echo "       Refusing to overwrite. Move/remove it manually first." >&2
  exit 1
fi

echo "==> Staging lurus-entkit into build context"
rm -rf "${KIT_DST}"
cp -r "${KIT_SRC}" "${KIT_DST}"
rm -rf "${KIT_DST}/.git"
touch "${STAMP}"

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
