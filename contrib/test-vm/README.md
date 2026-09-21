# Boot-testing the LUKS unlock

The unlock happens in an initrd, before the root filesystem is mounted and before sshd
exists. A loopback LUKS image can check that a token is written and read back correctly;
it cannot check that the agent is in the initramfs, that it finds a resolver, that it
answers the prompt, or that a human can still type the passphrase while it is trying.
Those need a real boot, which is what this builds.

Derived from the test scripts in [synctang](https://github.com/muelli/synctang), which is
where the approach comes from: build the disk with LUKS from the start out of the root
filesystem tarball, rather than shrinking and re-encrypting a cloud `.img`. Both projects
are AGPL-3.0-or-later.

## Using it

Needs root, because it loop-mounts and chroots. Everything lives under `$OUTDIR`; the only
state outside it is a loop device, detached on exit.

```bash
sudo env RELEASE=26.04 OUTDIR=/var/tmp/ss-vm-2604 bash ss-build-vm.sh
```

```bash
sudo env OUTDIR=/var/tmp/ss-vm-2604 BINARY=/path/to/syncthing-socket \
  CONTRIB=/path/to/repo/contrib SEED="$SEED" PEER_ID="$KEYHOLDER_SERVER_ID" \
  ROLE=client bash ss-provision-vm.sh
```

```bash
sudo env OUTDIR=/var/tmp/ss-vm-2604 MODE=relay SEED="$SEED" \
  BINARY=/path/to/syncthing-socket bash ss-test-unlock.sh
```

`RELEASE` picks the integration under test: 26.04 builds a dracut machine, 24.04 an
initramfs-tools one. `MODE=relay` starts a key holder and expects an unattended unlock.
`MODE=console` starts none, waits for the agent to try and fail, and only then types the
passphrase, which is the test that the agent does not block manual entry.

`ss-teardown.sh` removes everything. It refuses any `OUTDIR` outside `/var/tmp/ss-vm-*`.

## Things that are the way they are for a reason

**dracut is installed before the kernel.** The kernel's postinst builds whatever generator
is present, so the other order silently produces an initramfs-tools initrd and the dracut
module is never consulted.

**The initrd is rebuilt under `env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin`.** That is the
PATH a maintainer script and a kernel hook get. Rebuilding under it is what proves the
integration is findable in the situation that has now silently broken twice, once on each
generator. The provisioner then greps the initrd and fails if the binary is not in it.

**`linux-image-virtual`, not `-generic`.** This is a QEMU guest. `-generic` depends on
`linux-modules-extra` and the whole firmware set, over a gigabyte of drivers for hardware
that will never exist here, which overflows a small root filesystem partway through dpkg
and reads as a host disk-full error.

**The host mapper name and the guest's `/etc/crypttab` target agree.** `cryptsetup-initramfs`
traces the root device through the live device-mapper name to decide which crypttab entries
to bake in, so a mismatch yields an initrd with no crypt support and a boot that reaches
`ALERT! UUID=... does not exist` without ever asking for a passphrase. dracut hides this,
because it works off `rd.luks.uuid` instead.

**The console is a unix socket, not QEMU's `telnet:`.** Telnet negotiation injects option
bytes into a stream that is being matched against patterns, and only one client may attach
at a time: a second reader steals typed input, so a passphrase can vanish.

**`/etc/resolv.conf` is removed before being written** in the chroot. The cloud image ships
it as a symlink into `/run`, dangling in an unbooted image, so copying onto it fails and
every `apt-get` in the chroot then fails confusingly.
