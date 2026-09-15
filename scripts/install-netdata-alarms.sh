#!/usr/bin/env bash
# scripts/install-netdata-alarms.sh — install newhub's repo-owned netdata
# alarm definitions onto the HOST running netdata (R6). Run ON that host, as
# root or with sudo.
#
# WHAT THIS DOES NOT ASSUME: a git checkout of this repo on the host. It only
# needs health.d/newhub.conf reachable from one of, in order:
#   1. $SOURCE_HEALTH_D, if set — either the .conf file itself or a directory
#      containing it.
#   2. ./health.d/newhub.conf — i.e. this script and a sibling health.d/
#      directory were copied to the host together (scp -r
#      deploy/r6-host-netdata/{health.d,install helper} is one way to do
#      that; the README does not mandate a specific transport).
#   3. ../deploy/r6-host-netdata/health.d/newhub.conf, relative to this
#      script's own location — covers running it straight out of a full repo
#      checkout (e.g. while testing locally before it ever reaches R6).
# The first candidate that exists wins. Nothing here shells out to git.
#
# IDEMPOTENT: safe to re-run. It always copies the current source file over
# the installed one (so a stale host copy never lingers) and reloads
# netdata's health engine; it never appends, so re-running does not
# duplicate or accumulate alarms.
#
# Usage:
#   scripts/install-netdata-alarms.sh                 # install + reload
#   NETDATA_HEALTH_DIR=/custom/health.d  scripts/install-netdata-alarms.sh
#   SOURCE_HEALTH_D=/tmp/newhub.conf     scripts/install-netdata-alarms.sh
#
# This script does not touch health_alarm_notify.conf (alarm RECIPIENTS) —
# that is a separate, host-local owner action. See deploy/r6-host-netdata/README.md
# "Ownership boundary".

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
ALARM_FILENAME="newhub.conf"
NETDATA_HEALTH_DIR="${NETDATA_HEALTH_DIR:-/etc/netdata/health.d}"

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

if [ "$(id -u)" -ne 0 ]; then
	echo "install-netdata-alarms.sh: must run as root (or via sudo) to write $NETDATA_HEALTH_DIR" >&2
	exit 1
fi

mkdir -p "$NETDATA_HEALTH_DIR"
DEST_FILE="$NETDATA_HEALTH_DIR/$ALARM_FILENAME"

if [ -f "$DEST_FILE" ] && cmp -s "$SOURCE_FILE" "$DEST_FILE"; then
	echo "install-netdata-alarms.sh: $DEST_FILE already up to date"
else
	install -m 0644 "$SOURCE_FILE" "$DEST_FILE"
	echo "install-netdata-alarms.sh: installed $SOURCE_FILE -> $DEST_FILE"
fi

# Reload netdata's health engine without restarting the daemon (a restart
# would drop in-flight chart history). netdatacli is the modern path; fall
# back to SIGUSR2 (the documented signal for "reload health configuration")
# for older builds that ship without netdatacli. Either way this is
# best-effort: a host where netdata is not yet running should still end with
# the file installed, not a hard failure.
if command -v netdatacli >/dev/null 2>&1; then
	if netdatacli reload-health; then
		echo "install-netdata-alarms.sh: reloaded health config via netdatacli"
	else
		echo "install-netdata-alarms.sh: WARNING: netdatacli reload-health failed — reload manually" >&2
	fi
elif command -v netdata >/dev/null 2>&1 && pgrep -x netdata >/dev/null 2>&1; then
	if pkill -USR2 -x netdata; then
		echo "install-netdata-alarms.sh: reloaded health config via SIGUSR2"
	else
		echo "install-netdata-alarms.sh: WARNING: SIGUSR2 reload failed — reload manually" >&2
	fi
else
	echo "install-netdata-alarms.sh: WARNING: netdata does not appear to be running — file installed, nothing reloaded" >&2
fi

echo "install-netdata-alarms.sh: verify with: curl -s http://localhost:19999/api/v1/alarms?all | grep -o '\"newhub_[a-z_]*\"' | sort -u"
