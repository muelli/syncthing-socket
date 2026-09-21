#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Boot the disposable VM. Console and journal on separate unix sockets, ssh forwarded.
#
# Adapted from synctang's run-test-vm.sh. Two serial ports: the first is the console a
# harness watches and types at, the second carries only the journal, because a console
# also carrying every debug record is not readable, and the journal is exactly what is
# wanted when something fails before the root filesystem mounts.
set -euo pipefail
OUTDIR="${OUTDIR:?set OUTDIR}"
DISK="$OUTDIR/disk.img"
SSH_PORT="${SSH_PORT:-7122}"
MEM="${MEM:-2048}"
[ -f "$DISK" ] || { echo "no image at $DISK" >&2; exit 1; }
ACCEL=tcg; [ -r /dev/kvm ] && [ -w /dev/kvm ] && ACCEL=kvm
rm -f "$OUTDIR/console.sock" "$OUTDIR/journal.sock" "$OUTDIR/monitor.sock"
exec qemu-system-x86_64 \
	-accel "$ACCEL" -m "$MEM" -smp 2 \
	-drive "file=$DISK,format=raw,if=virtio" \
	-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22" \
	-device virtio-net-pci,netdev=net0 \
	-chardev "socket,id=con,path=$OUTDIR/console.sock,server=on,wait=off" -serial chardev:con \
	-chardev "socket,id=jrn,path=$OUTDIR/journal.sock,server=on,wait=off" -serial chardev:jrn \
	-monitor "unix:$OUTDIR/monitor.sock,server,nowait" \
	-display none "$@"
