#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Build a disposable VM with a real LUKS2 root, for testing the syncthing-socket
# unlock in the place it actually runs: an initrd.
#
# Adapted from synctang's make-test-vm.sh (AGPL-3.0-or-later, github.com/muelli/synctang),
# which is the origin of the approach: build encrypted from the start out of the root
# filesystem tarball, rather than shrinking and re-encrypting a cloud .img.
#
# Everything lives under $OUTDIR. The only state outside it is the loop device, detached
# on exit.
set -euo pipefail

RELEASE="${RELEASE:?set RELEASE to 24.04 or 26.04}"
OUTDIR="${OUTDIR:?set OUTDIR}"
DISK="$OUTDIR/disk.img"
DISK_SIZE="${DISK_SIZE:-6G}"
MNT="$OUTDIR/mnt"
ROOTFS_TAR="$OUTDIR/rootfs.tar.xz"
ROOTFS_URL="https://cloud-images.ubuntu.com/releases/$RELEASE/release/ubuntu-$RELEASE-server-cloudimg-amd64-root.tar.xz"
# The project's own test-fixture passphrase. This disk holds a stock Ubuntu and nothing
# else, and is rebuilt from scratch by this script.
VM_PASSPHRASE="${VM_PASSPHRASE:-passphrase}"
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

# Deliberately cheap key derivation: a stock LUKS2 header targets about two seconds of
# argon2id per keyslot trial, and paying that on every boot of a test measures argon2
# rather than the unlock. Never use these parameters for a real volume.
PBKDF_ARGS=(--pbkdf pbkdf2 --pbkdf-force-iterations 1000)

log() { printf '\n=== %s\n' "$*" >&2; }
[ "$(id -u)" -eq 0 ] || { echo "must run as root (loop-mounts and chroots)" >&2; exit 1; }
case "$RELEASE" in 24.04|26.04) ;; *) echo "RELEASE must be 24.04 or 26.04" >&2; exit 1 ;; esac

mkdir -p "$OUTDIR" "$MNT"
[ -f "$ROOTFS_TAR" ] || { log "downloading the $RELEASE root filesystem"; curl -sSL -o "$ROOTFS_TAR" "$ROOTFS_URL"; }

LOOP=""
cleanup() {
	set +e
	mountpoint -q "$MNT/dev/pts" && umount "$MNT/dev/pts"
	for d in dev proc sys run boot; do mountpoint -q "$MNT/$d" && umount -l "$MNT/$d"; done
	mountpoint -q "$MNT" && umount -l "$MNT"
	[ -e "$MAPPER" ] && cryptsetup close "$CRYPTNAME"
	[ -n "$LOOP" ] && losetup -d "$LOOP"
	return 0
}
trap cleanup EXIT

log "creating a $DISK_SIZE disk"
rm -f "$DISK"
truncate -s "$DISK_SIZE" "$DISK"

# BIOS boot, so no OVMF is needed anywhere: p1 is GRUB's BIOS boot partition, p2 is an
# unencrypted /boot, p3 is the LUKS2 container.
sgdisk --clear \
	--new=1:0:+2M --typecode=1:ef02 --change-name=1:biosboot \
	--new=2:0:+1G --typecode=2:8300 --change-name=2:boot \
	--new=3:0:0   --typecode=3:8309 --change-name=3:luks \
	"$DISK" > /dev/null

LOOP=$(losetup --find --show --partscan "$DISK")
log "loop device is $LOOP"

log "formatting the LUKS2 container"
printf '%s' "$VM_PASSPHRASE" | cryptsetup luksFormat --type luks2 "${PBKDF_ARGS[@]}" --batch-mode "${LOOP}p3" -
printf '%s' "$VM_PASSPHRASE" | cryptsetup open "${LOOP}p3" "$CRYPTNAME" -
LUKS_UUID=$(cryptsetup luksUUID "${LOOP}p3")

mkfs.ext4 -q -L boot "${LOOP}p2"
mkfs.ext4 -q -L root "$MAPPER"
mount "$MAPPER" "$MNT"
mkdir -p "$MNT/boot"
mount "${LOOP}p2" "$MNT/boot"

log "unpacking the root filesystem"
tar -xpf "$ROOTFS_TAR" -C "$MNT" --numeric-owner --xattrs-include='*'

BOOT_UUID=$(blkid -s UUID -o value "${LOOP}p2")
ROOT_UUID=$(blkid -s UUID -o value "$MAPPER")
cat > "$MNT/etc/fstab" <<EOF
UUID=$ROOT_UUID / ext4 defaults 0 1
UUID=$BOOT_UUID /boot ext4 defaults 0 2
EOF
cat > "$MNT/etc/crypttab" <<EOF
$CRYPTNAME UUID=$LUKS_UUID none luks,discard
EOF
touch "$MNT/etc/cloud/cloud-init.disabled"
echo "ss-test-$RELEASE" > "$MNT/etc/hostname"

mount --bind /dev "$MNT/dev"
mount --bind /dev/pts "$MNT/dev/pts"
mount -t proc proc "$MNT/proc"
mount -t sysfs sys "$MNT/sys"
mount -t tmpfs tmpfs "$MNT/run"
# The cloud image ships /etc/resolv.conf as a symlink into /run, dangling in an unbooted
# image, so copying onto it fails rather than replacing it and the chroot gets no DNS.
rm -f "$MNT/etc/resolv.conf"
cp /etc/resolv.conf "$MNT/etc/resolv.conf"

if [ "$RELEASE" = "26.04" ]; then
	# dracut must be present BEFORE the kernel: the kernel's postinst builds whatever
	# generator is installed, so the other order silently yields an initramfs-tools
	# initrd and the dracut module is never consulted.
	GEN_PKGS="dracut dracut-network cryptsetup cryptsetup-initramfs-"
else
	GEN_PKGS="initramfs-tools cryptsetup cryptsetup-initramfs"
fi

log "installing the kernel, $([ "$RELEASE" = 26.04 ] && echo dracut || echo initramfs-tools) and GRUB"
cat > "$MNT/tmp/setup.sh" <<CHROOT
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq $GEN_PKGS grub-pc openssh-server systemd-resolved ca-certificates qrencode
# linux-image-virtual, not -generic. This is a QEMU guest: virtual carries the virtio
# drivers and dm-crypt and little else, while generic Depends on linux-modules-extra and
# the whole linux-firmware set, which is over a gigabyte of drivers for hardware that will
# never exist here. With a 4G disk that overflowed the root filesystem partway through
# dpkg, which reads as a host disk-full error and is not one.
apt-get install -y -qq --no-install-recommends linux-image-virtual
# Say how much room is left inside the image. A full guest root reads as a dpkg
# "No space left on device", which looks like a host problem and is not one.
echo "--- guest root filesystem after the kernel:"
df -h / | tail -1

cat > /etc/default/grub <<'GRUB'
GRUB_DEFAULT=0
GRUB_TIMEOUT=2
GRUB_DISTRIBUTOR="ss-test"
GRUB_CMDLINE_LINUX_DEFAULT=""
GRUB_CMDLINE_LINUX="console=tty1 console=ttyS0,115200 rd.luks.uuid=$LUKS_UUID systemd.journald.forward_to_console=1"
GRUB_TERMINAL="console serial"
GRUB_SERIAL_COMMAND="serial --speed=115200"
GRUB
grub-install --target=i386-pc "$LOOP"
update-grub

# The journal goes to the second serial port so the first stays readable. TTYPath is not a
# kernel command line setting, so it has to be a drop-in, and dracut does not pull in
# arbitrary drop-ins beside journald.conf unless told to.
mkdir -p /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/99-ss-test.conf <<'JOURNALD'
[Journal]
ForwardToConsole=yes
TTYPath=/dev/ttyS1
MaxLevelConsole=debug
JOURNALD
mkdir -p /etc/dracut.conf.d
echo 'install_items+=" /etc/systemd/journald.conf.d/99-ss-test.conf "' > /etc/dracut.conf.d/99-ss-journal.conf

# initramfs-tools needs to be told to bring up networking; the dracut module writes
# rd.neednet=1 into the initrd itself.
if [ -f /etc/initramfs-tools/initramfs.conf ]; then
  sed -i 's/^#*IP=.*/IP=dhcp/' /etc/initramfs-tools/initramfs.conf
  grep -q '^IP=dhcp' /etc/initramfs-tools/initramfs.conf || echo 'IP=dhcp' >> /etc/initramfs-tools/initramfs.conf
fi

# openssh-server's postinst does not generate host keys in a chroot, and Ubuntu has no
# ssh-keygen.service to do it at first boot, so without this sshd fails to start on every
# boot and the forwarded ssh port goes nowhere.
ssh-keygen -A

systemctl enable ssh
systemctl enable serial-getty@ttyS0.service
echo "root:$VM_PASSPHRASE" | chpasswd
useradd -m -s /bin/bash -G sudo ubuntu || true
echo "ubuntu:$VM_PASSPHRASE" | chpasswd
mkdir -p /home/ubuntu/.ssh
CHROOT
chroot "$MNT" bash /tmp/setup.sh
rm -f "$MNT/tmp/setup.sh"

if [ -f "$OUTDIR/authorized_keys" ]; then
	install -o 1000 -g 1000 -m 600 "$OUTDIR/authorized_keys" "$MNT/home/ubuntu/.ssh/authorized_keys"
fi

log "built $DISK"
printf 'LUKS_UUID=%s\n' "$LUKS_UUID" | tee "$OUTDIR/vm.env"
printf 'RELEASE=%s\n' "$RELEASE" >> "$OUTDIR/vm.env"
