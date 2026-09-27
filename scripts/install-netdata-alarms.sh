#!/usr/bin/env bash
# scripts/install-netdata-alarms.sh — install newhub's repo-owned netdata
# alarm definitions onto the R6 host. Run ON that host.
#
# WHERE THIS WRITES: R6's netdata runs as the `obs-netdata` CONTAINER, whose
# health config is bind-mounted PER FILE from
# /data/obs-pack/runtime/netdata/config/health.d/newhub.conf on the host into
# /etc/netdata/health.d/newhub.conf inside the container — writing directly
# to /etc/netdata/health.d on the host writes nowhere the container reads,
# and dropping a NEW file into the host directory is not picked up either
# (the mount is per-file, not per-directory). The default destination below
# is the bind SOURCE, not the container path; do not override
# NETDATA_HEALTH_DIR to /etc/netdata/health.d on this host.
#
# WHAT THIS DOES NOT ASSUME: a git checkout of this repo on the host. It only
# needs health.d/newhub.conf reachable from one of, in order:
#   1. $SOURCE_HEALTH_D, if set — either the .conf file itself or a directory
#      containing it.
#   2. ./health.d/newhub.conf — i.e. this script and a sibling health.d/
#      directory were copied to the host together (scp -r
#      deploy/r6-host-netdata/{health.d,install helper} is one way to do
#      that; no specific transport is mandated).
#   3. ../deploy/r6-host-netdata/health.d/newhub.conf, relative to this
#      script's own location — covers running it straight out of a full repo
#      checkout (e.g. while testing locally before it ever reaches R6).
# The first candidate that exists wins. Nothing here shells out to git.
#
# IDEMPOTENT: safe to re-run. It always copies the current source file over
# the installed one (so a stale host copy never lingers) and reloads
# netdata's health engine inside the container; it never appends, so
# re-running does not duplicate or accumulate alarms.
#
# ROOT: only required if the destination directory is not writable by the
# current user (checked, not assumed) — on a host where /data/obs-pack is
# group-writable to the deploying user, this script runs without sudo.
#
# RELOAD: netdata runs inside the `obs-netdata` container, so reload goes
# through `docker exec obs-netdata netdatacli reload-health`. If that
# container is not running, this is a HARD FAILURE (exit nonzero), not a
# warning — a file installed but never reloaded is silently stale, exactly
# the "looks live but isn't" state this lane exists to prevent. Use
# INSTALL_ONLY=1 to skip the reload step deliberately (e.g. staging the file
# before the container exists yet).
#
# Usage:
#   scripts/install-netdata-alarms.sh                 # install + reload
#   NETDATA_HEALTH_DIR=/custom/health.d  scripts/install-netdata-alarms.sh
#   SOURCE_HEALTH_D=/tmp/newhub.conf     scripts/install-netdata-alarms.sh
#   INSTALL_ONLY=1                       scripts/install-netdata-alarms.sh
#
# This script does not touch health_alarm_notify.conf (alarm RECIPIENTS) —
# that is a separate, host-local owner action. See deploy/r6-host-netdata/README.md
# "Ownership boundary".

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
ALARM_FILENAME="newhub.conf"
NETDATA_HEALTH_DIR="${NETDATA_HEALTH_DIR:-/data/obs-pack/runtime/netdata/config/health.d}"
NETDATA_CONTAINER="${NETDATA_CONTAINER:-obs-netdata}"

resolve_source() {
	if [ -n "${SOURCE_HEALTH_D:-}" ]; then
		if [ -f "$SOURCE_HEALTH_D" ]; then
			printf '%s\n' "$SOURCE_HEALTH_D"
			return 0
		fi
		if [ -f "$SOURCE_HEALTH_D/$ALARM_FILENAME" ]; then
			printf '%s\n' "$SOURCE_HEALTH_D/$ALARM_FILENAME"
			return 0
		fi
		echo "install-netdata-alarms.sh: SOURCE_HEALTH_D=$SOURCE_HEALTH_D set but no $ALARM_FILENAME found there" >&2
		return 1
	fi
	if [ -f "$SCRIPT_DIR/health.d/$ALARM_FILENAME" ]; then
		printf '%s\n' "$SCRIPT_DIR/health.d/$ALARM_FILENAME"
		return 0
	fi
	if [ -f "$SCRIPT_DIR/../deploy/r6-host-netdata/health.d/$ALARM_FILENAME" ]; then
		printf '%s\n' "$SCRIPT_DIR/../deploy/r6-host-netdata/health.d/$ALARM_FILENAME"
		return 0
	fi
	return 1
}

SOURCE_FILE="$(resolve_source)" || {
	echo "install-netdata-alarms.sh: could not find $ALARM_FILENAME — set SOURCE_HEALTH_D explicitly (see script header)" >&2
	exit 1
}

# Root is only required when the destination isn't writable by the current
# user — checked directly rather than assumed from id -u, so a host where
# the deploying user already owns/can-write /data/obs-pack does not need
# sudo at all, and a local rehearsal against a scratch directory never needs
# root either.
if [ -d "$NETDATA_HEALTH_DIR" ]; then
	dir_check="$NETDATA_HEALTH_DIR"
else
	dir_check="$(dirname -- "$NETDATA_HEALTH_DIR")"
fi
if [ ! -w "$dir_check" ] && [ "$(id -u)" -ne 0 ]; then
	echo "install-netdata-alarms.sh: $NETDATA_HEALTH_DIR is not writable by $(id -un) — run as root (or via sudo)" >&2
	exit 1
fi

mkdir -p "$NETDATA_HEALTH_DIR"
DEST_FILE="$NETDATA_HEALTH_DIR/$ALARM_FILENAME"

if [ -f "$DEST_FILE" ] && cmp -s "$SOURCE_FILE" "$DEST_FILE"; then
	echo "install-netdata-alarms.sh: $DEST_FILE already up to date"
else
	# Write IN PLACE (truncate + rewrite the existing inode). The container's
	# bind mount is per-file, i.e. pinned to the inode that existed when the
	# container started; `install`/`mv` unlink and recreate, so the container
	# kept reading the old file while the host showed the new one. That is
	# exactly what happened here from 2026-09-16 to 2026-09-27: 8 alarms live
	# while the host copy said 31, then 38, and every reload was a no-op.
	if [ -f "$DEST_FILE" ]; then
		cat "$SOURCE_FILE" > "$DEST_FILE"
	else
		install -m 0644 "$SOURCE_FILE" "$DEST_FILE"
	fi
	echo "install-netdata-alarms.sh: installed $SOURCE_FILE -> $DEST_FILE"
fi

if [ "${INSTALL_ONLY:-0}" = "1" ]; then
	echo "install-netdata-alarms.sh: INSTALL_ONLY=1 set — skipping reload"
	exit 0
fi

# Reload netdata's health engine without restarting the daemon (a restart
# would drop in-flight chart history). netdata runs inside the
# $NETDATA_CONTAINER container on this host — reload has to go through
# `docker exec`, there is no host-level netdata process or netdatacli to
# fall back to. A missing container is a HARD FAILURE: the file is on disk
# but never reloaded is indistinguishable from "the condition hasn't
# happened yet" until the next full container restart, which is exactly the
# silent-failure shape this lane exists to eliminate elsewhere. Use
# INSTALL_ONLY=1 (above) if that is genuinely what's wanted.
if ! command -v docker >/dev/null 2>&1; then
	echo "install-netdata-alarms.sh: docker not found on PATH — cannot reload $NETDATA_CONTAINER. File is installed at $DEST_FILE but NOT reloaded; set INSTALL_ONLY=1 to acknowledge this deliberately." >&2
	exit 1
fi
if ! docker inspect -f '{{.State.Running}}' "$NETDATA_CONTAINER" 2>/dev/null | grep -q true; then
	echo "install-netdata-alarms.sh: container $NETDATA_CONTAINER is not running — cannot reload. File is installed at $DEST_FILE but NOT reloaded; set INSTALL_ONLY=1 to acknowledge this deliberately." >&2
	exit 1
fi
if docker exec "$NETDATA_CONTAINER" netdatacli reload-health; then
	echo "install-netdata-alarms.sh: reloaded health config via 'docker exec $NETDATA_CONTAINER netdatacli reload-health'"
else
	echo "install-netdata-alarms.sh: docker exec $NETDATA_CONTAINER netdatacli reload-health FAILED — file installed at $DEST_FILE but the running config is stale until this is resolved" >&2
	exit 1
fi

# A reload only re-reads what the container can see. The bind mount is
# per-file, pinned to the inode present when the container started; if the
# host file was ever replaced (rather than rewritten in place) the container
# still reads the OLD inode and every reload above re-reads the old file.
# Compare inodes — they are the same filesystem — and refuse to report
# success when they differ: the only repair is a container restart.
host_inode="$(stat -c %i "$DEST_FILE")"
ctr_inode="$(docker exec "$NETDATA_CONTAINER" stat -c %i "/etc/netdata/health.d/$ALARM_FILENAME" 2>/dev/null || echo unknown)"
if [ "$host_inode" != "$ctr_inode" ]; then
	echo "install-netdata-alarms.sh: STALE BIND — host $DEST_FILE is inode $host_inode but $NETDATA_CONTAINER reads inode $ctr_inode. The reload re-read the OLD file. Restart the container (docker restart $NETDATA_CONTAINER) and re-run." >&2
	exit 1
fi
echo "install-netdata-alarms.sh: container reads the installed inode ($host_inode)"

echo "install-netdata-alarms.sh: verify with: curl -s http://localhost:19999/api/v1/alarms?all | grep -o '\"newhub_[a-z_]*\"' | sort -u"
