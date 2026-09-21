#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Remove everything this harness created. Only ever touches $OUTDIR.
set -euo pipefail
OUTDIR="${OUTDIR:?set OUTDIR}"
case "$OUTDIR" in /var/tmp/ss-vm-*) ;; *) echo "refusing: OUTDIR must be /var/tmp/ss-vm-*" >&2; exit 1 ;; esac
pkill -f "$OUTDIR/disk.img" 2>/dev/null || true
pkill -f "keyholder-$OUTDIR" 2>/dev/null || true
sleep 1
for d in dev/pts dev proc sys run boot; do mountpoint -q "$OUTDIR/mnt/$d" && umount -l "$OUTDIR/mnt/$d"; done 2>/dev/null || true
mountpoint -q "$OUTDIR/mnt" && umount -l "$OUTDIR/mnt" || true
for m in /dev/mapper/sstest*; do [ -e "$m" ] && cryptsetup close "$(basename "$m")"; done 2>/dev/null || true
losetup -j "$OUTDIR/disk.img" 2>/dev/null | cut -d: -f1 | xargs -r -n1 losetup -d || true
rm -rf "$OUTDIR"
echo "removed $OUTDIR"
