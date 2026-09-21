#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Install syncthing-socket into the disposable VM and enrol it, then rebuild the initrd.
#
# Enrolment happens here, against the image's LUKS2 partition over a loop device, rather
# than inside the VM: it is the same cryptsetup on the same header either way, and this
# way the VM never has to be booted just to be set up.
set -euo pipefail

OUTDIR="${OUTDIR:?set OUTDIR}"
BINARY="${BINARY:?set BINARY to the syncthing-socket binary}"
CONTRIB="${CONTRIB:?set CONTRIB to the repo's contrib/ directory}"
SEED="${SEED:?set SEED}"
PEER_ID="${PEER_ID:?set PEER_ID to the key holder's Device ID}"
ROLE="${ROLE:-client}"
VM_PASSPHRASE="${VM_PASSPHRASE:-passphrase}"
DISK="$OUTDIR/disk.img"
MNT="$OUTDIR/mnt"
# Host-side mapper name, unique per VM so two builds can run at once.
# The name inside the guest's crypttab stays "cryptroot".
. "$OUTDIR/vm.env"
# One name for both the host-side mapper and the guest's crypttab target, and unique per
# release so two releases can still be built at once.
#
# They have to agree. cryptsetup-initramfs traces the root device back through the live
# device-mapper name when it decides which crypttab entries to bake into the initrd, so a
# host mapper called something else means it finds no entry, builds an initrd with no
# crypt setup, and the machine boots to "ALERT! UUID=... does not exist. Dropping to a
# shell!" having never asked for a passphrase. dracut hides this, because it works off
# rd.luks.uuid instead.
CRYPTNAME="ssroot$(echo "${RELEASE:-x}" | tr -d .)"
MAPPER="/dev/mapper/$CRYPTNAME"

log() { printf '\n=== %s\n' "$*" >&2; }
[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }

LOOP=""
cleanup() {
	set +e
	for d in dev/pts dev proc sys run boot; do mountpoint -q "$MNT/$d" && umount -l "$MNT/$d"; done
	mountpoint -q "$MNT" && umount -l "$MNT"
	[ -e "$MAPPER" ] && cryptsetup close "$CRYPTNAME"
	[ -n "$LOOP" ] && losetup -d "$LOOP"
	return 0
}
trap cleanup EXIT

LOOP=$(losetup --find --show --partscan "$DISK")
printf '%s' "$VM_PASSPHRASE" | cryptsetup open "${LOOP}p3" "$CRYPTNAME" -
mount "$MAPPER" "$MNT"
mount "${LOOP}p2" "$MNT/boot"

# /usr/sbin, not /usr/local/bin: that is the PATH dpkg maintainer scripts and kernel hooks
# run with, and the Debian packaging installs it there for the same reason.
log "installing the binary and the initramfs integration"
install -m755 "$BINARY" "$MNT/usr/sbin/syncthing-socket"
install -m755 "$CONTRIB/initramfs-luks/syncthing-luks-bind" "$MNT/usr/sbin/syncthing-luks-bind"
install -m755 "$CONTRIB/initramfs-luks/syncthing-luks-setup" "$MNT/usr/sbin/syncthing-luks-setup"

if [ "$RELEASE" = "26.04" ]; then
	mkdir -p "$MNT/usr/lib/dracut/modules.d/90syncthing-socket"
	install -m755 "$CONTRIB/dracut-luks/90syncthing-socket"/*.sh \
		"$MNT/usr/lib/dracut/modules.d/90syncthing-socket/"
else
	install -m755 "$CONTRIB/initramfs-luks/syncthing-socket-hook" \
		"$MNT/etc/initramfs-tools/hooks/syncthing-socket"
	install -m755 "$CONTRIB/initramfs-luks/syncthing-socket-initramfs-top" \
		"$MNT/etc/initramfs-tools/scripts/local-top/syncthing-socket"
	install -m755 "$CONTRIB/initramfs-luks/syncthing-socket-initramfs-bottom" \
		"$MNT/etc/initramfs-tools/scripts/local-bottom/syncthing-socket"
fi

log "binding the unlock configuration into the LUKS2 header"
# Run with the HOST's PATH, deliberately. An earlier version prepended the guest's
# /usr/sbin so the helper would be findable, which was pointless because it is invoked by
# absolute path, and harmful: it shadowed the host's cryptsetup with the guest's. A 26.04
# cryptsetup binary against a 24.04 host library fails with "version CRYPTSETUP_2.8 not
# found", and the script then reports the device is not LUKS2, which it is. The loop
# device belongs to the host, so the host's cryptsetup is the right one.
"$MNT/usr/sbin/syncthing-luks-bind" "${LOOP}p3" "$SEED" "$PEER_ID" "$ROLE"
echo "--- token as written:"
cryptsetup token export --token-id 0 "${LOOP}p3"; echo

mount --bind /dev "$MNT/dev"
mount --bind /dev/pts "$MNT/dev/pts"
mount -t proc proc "$MNT/proc"
mount -t sysfs sys "$MNT/sys"
mount -t tmpfs tmpfs "$MNT/run"

KVER=$(basename "$(find "$MNT/lib/modules" -mindepth 1 -maxdepth 1 -type d | sort | tail -1)")
log "rebuilding the initrd for $KVER under a maintainer-script PATH"
# env -i with a minimal PATH on purpose. This is the situation that silently broke before:
# a kernel upgrade rebuilding the initrd without /usr/local/bin on PATH, where the dracut
# module's check() failed and the agent was simply absent from the result.
if [ "$RELEASE" = "26.04" ]; then
	chroot "$MNT" env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin \
		dracut --force --quiet "/boot/initrd.img-$KVER" "$KVER"
else
	chroot "$MNT" env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin \
		update-initramfs -u -k "$KVER"
fi

log "checking the agent actually reached the initrd"
if [ "$RELEASE" = "26.04" ]; then
	chroot "$MNT" lsinitrd "/boot/initrd.img-$KVER" | grep -E "syncthing" || {
		echo "FAIL: the rebuilt initrd contains no syncthing-socket files" >&2; exit 1; }
else
	chroot "$MNT" lsinitramfs "/boot/initrd.img-$KVER" | grep -E "syncthing" || {
		echo "FAIL: the rebuilt initramfs contains no syncthing-socket files" >&2; exit 1; }
fi
log "provisioned"
