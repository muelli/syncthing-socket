# Unlocking a LUKS root volume with syncthing-socket

Unlock an encrypted root filesystem during early boot on a machine that **cannot accept
inbound connections**: a VM behind NAT, a server on a hotel network, a box whose firewall
you do not control. The booting machine only ever dials out.

- **Booting machine:** an initramfs `local-top` script obtains the passphrase and writes it
  into `/lib/cryptsetup/passfifo`, the same FIFO cryptsetup's own passphrase prompt reads
  from.
- **Key holder (your laptop or phone):** you supply the passphrase on demand.

Either side can be the one that dials, and both are outbound-only, so NAT is not a problem
in either direction. `UNLOCK_ROLE` picks:

| Role | Booting machine runs | Key holder runs | Use for |
| --- | --- | --- | --- |
| `client` (default) | `syncthing-socket client` | `syncthing-socket server`, passphrase piped in | a laptop you can leave listening |
| `server` | `syncthing-socket server` | `syncthing-socket client`, passphrase piped in | the Android app |

The Android app dials out, so a phone-unlocked machine uses `UNLOCK_ROLE=server`. That fits
Android: the phone acts for a few seconds when you tap unlock, instead of holding a
listening service open in the background while a machine boots.

The two find each other through Syncthing's global discovery and public relay network, then
upgrade to a direct peer-to-peer link over ICE/WebRTC when the network allows it. Identities
are Syncthing Device IDs derived from a shared seed, so nothing has to be provisioned beyond
that seed.

This follows the approach Clevis uses, and the reason matters: it does **not** install a
cryptsetup `keyscript=`. A keyscript *replaces* cryptsetup's askpass, which breaks manual
passphrase entry and `cryptroot-unlock`. Racing the normal prompt instead means console
entry, `cryptroot-unlock`, Clevis and TPM agents all keep working. Whichever key arrives
first wins, and `/etc/crypttab` needs no changes at all.

> Verified end to end on Ubuntu 24.04 (cryptsetup 2.7.0, initramfs-tools 0.142) with an
> unencrypted `/boot` partition and BIOS boot.

## Requirements

- Debian/Ubuntu with either **initramfs-tools** or **dracut**. mkinitcpio is not
  supported. **Pick the right package**, because installing the wrong one fails silently:

  ```bash
  dpkg -S /usr/sbin/update-initramfs
  ```

  `initramfs-tools` means `syncthing-socket-luks-initramfs`; `dracut` means
  `syncthing-socket-luks-dracut`. Ubuntu 26.04 and later are dracut, and they leave the
  initramfs-tools packages installed beside it, which is what makes the mistake so easy.
  On those releases `update-initramfs` is a dracut wrapper: it prints "Generating
  /boot/initrd.img-...", exits 0, and ignores everything under `/etc/initramfs-tools`. The
  hook lands on disk, nothing reports an error, and the machine sits at its passphrase
  prompt at the next boot.

  The pairing, `syncthing-luks-bind`, the LUKS2 token, `/etc/crypttab` and the threat
  models are the same either way. What differs is the package you install, how networking
  is configured, the command that rebuilds the initramfs, how you turn up the logging, and
  the delivery mechanism itself. Each of those says so where it comes up below.
- A **separate unencrypted `/boot`**. GRUB never touches the encrypted volume, so
  `GRUB_ENABLE_CRYPTODISK` is not needed.
- **Networking in the initramfs**, with working DNS; discovery and the relay pool are
  reached over HTTPS.
- `ca-certificates` installed when the initramfs is built; the trust store is copied in.

## Install

From the signed repository:

```bash
sudo curl -fsSL https://muelli.github.io/syncthing-socket/deb/KEY.gpg -o /etc/apt/keyrings/syncthing-socket.gpg
```

```bash
echo "deb [signed-by=/etc/apt/keyrings/syncthing-socket.gpg] https://muelli.github.io/syncthing-socket/deb stable main" | sudo tee /etc/apt/sources.list.d/syncthing-socket.list
```

```bash
sudo apt update && sudo apt install syncthing-socket syncthing-socket-luks-initramfs
```

On dracut systems, Ubuntu 26.04 and later, install `syncthing-socket-luks-dracut` in place
of `syncthing-socket-luks-initramfs`. Nothing else on this page changes.

Or from downloaded files:

```bash
sudo apt install ./syncthing-socket_*.deb ./syncthing-socket-luks-initramfs_*.deb
```

Or, from a checkout, `just install && sudo just install-contrib`, or
`just install && sudo just install-contrib-dracut` on a dracut system. (`just install` on
its own deliberately skips the initramfs pieces; they rewrite your boot path.)

Enable networking in the initramfs, in `/etc/initramfs-tools/initramfs.conf`:

```
IP=dhcp
```

A **static** address is better in production. It removes a DHCP round trip from the critical
path, and, as *Gotchas* explains, the initramfs and the booted system do not get the same
lease anyway. Set it on the kernel command line instead of `IP=dhcp`:

```
ip=192.0.2.10::192.0.2.1:255.255.255.0::enp1s0:off
```

**On dracut** there is nothing to enable: the module writes `rd.neednet=1` into the
initramfs itself, and DHCP on every interface is already the default, so the network is up
and waited for without any configuration. The same `ip=` syntax works on the kernel command
line for a static address. There is one wrinkle worth knowing: an initramfs built without
`systemd-resolved`, which is what Ubuntu 26.04 produces, has no `/etc/resolv.conf` and
nothing that writes one, so the agent reads the nameserver out of systemd-networkd's own
lease and writes the file itself. It says so on the console when it does.

## Configure

Pick a seed and see what identities it produces:

```bash
syncthing-socket id --passphrase "your-key-seed"
```

### Recommended: asymmetric identities

The initramfs lives on an **unencrypted** `/boot`, so treat everything in it as public.
Give the booting machine its own seed, and pin the key holder by its Device ID:

```bash
# On the key holder, once. Its Device ID is public information.
syncthing-socket id --passphrase "$KEYHOLDER_SEED"     # -> Server ID

# On the booting machine, store the config in the LUKS2 header:
sudo syncthing-luks-bind /dev/nvme0n1p3 "$BOOT_SEED" "$KEYHOLDER_SERVER_ID"
```

Then the key holder answers only that machine:

```bash
printf %s 'your-luks-passphrase' | syncthing-socket server \
    --passphrase "$KEYHOLDER_SEED" \
    --authorized-clients "$BOOT_CLIENT_ID"      # id --passphrase "$BOOT_SEED" -> Client ID
```

Now the initramfs contains only the booting machine's *own* identity plus a public Device
ID. Someone who reads it cannot impersonate your key holder.

### Simpler: one shared seed

Both ends derive everything from the same seed. The booting machine uses
`seed + "client"`, the key holder `seed + "server"`:

```bash
sudo syncthing-luks-bind /dev/nvme0n1p3 "your-key-seed"
printf %s 'your-luks-passphrase' | syncthing-socket server --passphrase "your-key-seed"
```

Convenient, but anyone who reads your initramfs learns the seed and can derive **both**
identities.

### Where the configuration lives

`syncthing-luks-bind` writes a token into the LUKS2 header. Check it with:

```bash
sudo cryptsetup token export --token-id 0 /dev/nvme0n1p3
```

If you would rather not touch the header, put the same values in
`/etc/syncthing-socket/luks.conf` instead. The hook copies it into the initramfs, and it
takes precedence over the token:

```sh
P2P_KEY_SEED="..."
KEY_BEARING_DEVICE_ID="..."   # optional
UNLOCK_ROLE="client"          # or "server"
```

Either way the seed is stored **in cleartext**, in the header or in an unencrypted
initramfs. It is not a secret store.

## Activate

Nothing to change in `/etc/crypttab`; a stock entry is what you want:

```
cryptroot UUID=<luks-uuid> none luks,discard
```

Just rebuild and reboot:

```bash
sudo update-initramfs -u -k all
```

**On dracut**, rebuild with:

```bash
sudo dracut --force --regenerate-all
```

Installing the package does this for you; you only need it by hand after editing the
configuration. Confirm the module actually made it in, since a module that fails its
`check()` is skipped silently:

```bash
sudo lsinitrd /boot/initrd.img-$(uname -r) | grep syncthing-socket
```

## Unlock

With the default `client` role, start the key holder **before** you reboot and leave it
running:

```bash
read -rsp 'LUKS passphrase: ' PASS
printf %s "$PASS" | syncthing-socket server --passphrase "$KEYHOLDER_SEED"
```

With `UNLOCK_ROLE=server` the machine waits instead, and you push the passphrase whenever
you like (this is what the Android app does):

```bash
read -rsp 'LUKS passphrase: ' PASS
printf %s "$PASS" | syncthing-socket client --passphrase "$SEED" "$MACHINE_DEVICE_ID"
```

The server consumes its stdin **once** and exits after the transfer, so each unlock needs a
fresh invocation. Give it a head start: a fresh announcement takes roughly 30–45 seconds to
become visible in global discovery.

Boot progress is printed to the console:

```
syncthing-socket: unlock attempt 1 ...
cryptsetup: cryptroot: set up successfully
```

## Unlocking from an Android phone

The phone dials out, so the machine takes the `server` role: it announces from its
initramfs and waits, and the phone pushes the passphrase when you tap unlock. That suits
Android, which cannot reliably hold a listening service open in the background while a
machine boots.

Both identities come from one seed. The machine uses `seed + "server"`, the phone uses
`seed + "client"`, so a single QR code carries everything the phone needs.

### 1. Enrol, on the machine

```bash
sudo syncthing-luks-setup /dev/nvme0n1p3
```

It asks for the LUKS passphrase, generates a seed, derives both Device IDs, prints a QR
code, and prints the exact `syncthing-luks-bind` command to run next. That command stores
the seed, the phone's Device ID and `unlock_role: server` in the LUKS2 header:

```bash
sudo syncthing-luks-bind /dev/nvme0n1p3 "<seed>" "<phone-device-id>" server
sudo update-initramfs -u -k all
```

Check what was stored:

```bash
sudo cryptsetup token export --token-id 0 /dev/nvme0n1p3
```

**The QR code contains your LUKS passphrase in clear text.** Scan it directly off the
screen. Do not photograph it, screenshot it, or put it in a chat.

### 2. Pair the phone

Install the app and open it. A phone with no pairing shows only the setup screen, so
there is no unlock button to press by accident. Tap **Scan the QR code** and point the
camera at the terminal. If the camera is unavailable or the QR code will not read, tap
**Enter the details as text** and type the three values `syncthing-luks-setup` printed
underneath the QR code.

The passphrase, the seed and the machine's Device ID are stored under a key that requires
your screen lock or a strong biometric, so the phone asks you to confirm before it saves
them. Nothing is sent anywhere yet.

The app has no way to delete a pairing or to hold a second one; clear its app data in
Android's settings to start over, then see *Re-enrolling a phone* below. To unlock a second
machine, install a second copy of the app with its own application id, as described under
*Unlocking more than one computer* in the top-level [README](../../README.md).

### 3. Unlock

Reboot the machine. It brings up networking in the initramfs, announces itself, and waits,
printing to the console:

```
syncthing-socket: requesting the passphrase for /dev/nvme0n1p3
```

Open the app, tap **Unlock**, confirm with your fingerprint or face. The phone connects
over the Syncthing relay network and pushes the passphrase. The machine prints:

```
syncthing-socket: passphrase delivered for /dev/nvme0n1p3
cryptsetup: cryptroot: set up successfully
```

and carries on booting.

### Re-enrolling a phone

A phone that has lost its pairing (app data cleared, a replacement handset, a reinstall)
does not need the machine reconfigured. The seed lives in the LUKS2 header, so it can be
read back and printed again:

```bash
sudo syncthing-luks-setup --reprint /dev/nvme0n1p3
```

That asks for the disk passphrase, verifies it, and prints the same QR code and the same
three values as the original enrolment. Nothing in the header changes, so there is no
re-bind and no `update-initramfs`, and any other phone still holding this pairing keeps
working.

If the old phone still works, it can do this itself: **Pair another phone** on its unlock
screen shows the same code, after it has confirmed your identity. That is the easier route
when the machine is not currently reachable, which is often exactly when you need it.

Re-run `syncthing-luks-setup` **without** `--reprint` only when you want to rotate, for
instance because a phone was lost and you want its pairing to stop working. That mints a
fresh seed, so you must run the `syncthing-luks-bind` line it prints and rebuild the
initramfs; until you do, the header still names the old phone. Rotating the seed does not
change the disk passphrase, so if that is what leaked, change it with `cryptsetup
luksChangeKey` and enrol again afterwards.

### If it does not unlock

The machine keeps retrying, so you can take your time. In order of likelihood:

- **The machine has not announced yet.** A fresh announcement takes 30 to 45 seconds to
  become visible in global discovery. Watch the console for the attempt counter.
- **No network in the initramfs.** The console shows the DHCP lease. No lease means no
  unlock; see *Gotchas* about the initramfs getting a different lease from the booted
  system.
- **Wrong pairing.** Confirm the phone's Device ID matches what the header authorises:
  `syncthing-socket id --passphrase "<seed>"` prints the Client ID, which must equal
  `key_bearing_device_id` in the token.

You always have the fallbacks below: the console prompt and `cryptroot-unlock` both keep
working, because this integration races cryptsetup's own prompt rather than replacing it.

## Keep a fallback

Install `dropbear-initramfs` and put a key in `/etc/dropbear/initramfs/authorized_keys`. If
the network path fails you can still get in:

```bash
ssh -i ~/.ssh/initramfs_key root@<initramfs-ip>
```

From that shell, `cryptroot-unlock` works normally:

```bash
echo -n 'your-luks-passphrase' | cryptroot-unlock
```

So does typing the passphrase at the console prompt. Both keep working precisely because
this integration leaves cryptsetup's askpass in charge instead of replacing it with a
keyscript.

**On dracut** the console prompt works the same way and for the same reason, but
`dropbear-initramfs` and `cryptroot-unlock` are initramfs-tools tools and do not exist
there. The equivalents are dracut's own `rd.break` shells and `systemd-ask-password`. The
console prompt is the fallback that is present either way, so it is the one worth testing
before you rely on any of this.

## Gotchas

- **The initramfs and the booted system get different DHCP leases.** `dhcpcd` in the
  initramfs identifies itself differently from systemd-networkd, so the machine appears at
  one address during boot and another afterwards. Do not assume they match; use a static
  `ip=` if it matters.
- **The `local-top` script must declare no `PREREQ`.** Naming `cryptroot` orders it *after*
  the script that blocks waiting for the passphrase, so the loop never starts and the boot
  hangs forever. It has to be running and polling while askpass waits.
- **Global discovery accumulates relay addresses** across key-holder restarts and does not
  return the newest first. syncthing-socket tries every address; if you are debugging with
  an older build, pin one end with `--relay` to sidestep it.
- **Retries are expected.** The client backs off exponentially to 60 s. Attempts before the
  key holder is announced fail by design.
- Behind a proxy, the client honours `HTTPS_PROXY`/`SOCKS_PROXY`, but the `local-top` script
  does not set them; export them there yourself.

## Debugging

Raise the client's verbosity by editing `--log-level error` to `--log-level info` in
`/etc/initramfs-tools/scripts/local-top/syncthing-socket`; its stderr already goes to
`/dev/console`. To see the whole boot on a VM, add
`console=ttyS0,115200` to the kernel command line and watch `virsh console`.

**On dracut** the agent already logs to the console, including which device it picked, what
it did about a missing resolver, and the last line of any failed transfer. `rd.syncthing_socket=0`
on the kernel command line turns it off for one boot, for when the network path is what is
broken and you just want the prompt back.

Check what discovery currently advertises for your key holder:

```bash
curl -s "https://discovery-lookup.syncthing.net/v2/?device=$KEYHOLDER_SERVER_ID"
```

## How this differs from Clevis

The integration is modelled on Clevis, but what goes into the LUKS2 header is not the same
kind of thing, and it is worth being clear about it.

Clevis stores a **JWE** in the header: the volume key, encrypted under a policy. The policy
is the metadata: a `tang` pin, a `tpm2` pin, or an `sss` pin wrapping *n* sub-pins with a
threshold *t*, which is how "2 of these 3 Tang servers must answer" is expressed. The key
material is genuinely present on disk, and unlocking means satisfying the policy well enough
to decrypt it; no server ever learns the key.

syncthing-socket stores **no key material at all**. The token holds a seed and, optionally, a
peer Device ID; routing and identity, not ciphertext. The passphrase lives with the key
holder and is sent over the wire when they choose to send it.

The trade-off runs both ways:

- Weaker: anyone who reads the header or the initramfs gets an identity your key holder
  authorises, and can therefore *ask* for the passphrase. Clevis's header is useless without
  also satisfying the policy.
- Stronger: the passphrase is not on the disk in any form, and the holder is a human (or a
  phone) who can simply decline. A stolen laptop cannot be unlocked by a thief who also
  seizes the Tang server.
- Different: there is no threshold. One key holder answers, or nobody does. A `t`-of-`n`
  equivalent would need the passphrase split across several holders with secret sharing so
  that no single one can unlock alone. Worth doing, but it is a real feature rather than a
  config option, and it is not implemented.

Because this does not use a keyscript, it composes with Clevis rather than competing: run
both, and whichever answers the prompt first wins.

### How the answer is delivered

Same goal on both generators, different plumbing, because the two initrds ask in entirely
different ways.

On **initramfs-tools**, a `local-top` script finds the running `/lib/cryptsetup/askpass`,
reads the `/lib/cryptsetup/passfifo` it is waiting on out of `/proc/<pid>/fd`, and writes
the passphrase into it. The device being asked about comes from `CRYPTTAB_SOURCE` in that
process's environment.

On **dracut**, there is no askpass and no passfifo. `systemd-cryptsetup` asks through
systemd's password agent protocol, so `syncthing-socket luks-agent` runs as one more agent:
it watches `/run/systemd/ask-password/` for `ask.*` requests and replies on the datagram
socket each one names. Several agents may answer the same request and the first useful
answer wins, which is the same property the passfifo race relies on.

One difference is worth knowing about. systemd sets no `CRYPTTAB_SOURCE`, so the dracut
agent cannot be told which device is being asked about; it scans the block devices for a
LUKS2 header carrying a `syncthing-socket` token and uses the first one it finds. With a
single encrypted volume, the documented case, this is equivalent. With two of them both
carrying our token, it would answer with the wrong one's configuration.

`rd.syncthing_socket=0` on the kernel command line disables the agent for one boot, for
when the network path is what is broken and you just want the console prompt.

## Security notes

- **The seed in the LUKS2 header is not the passphrase, but it is sensitive.** It is 32
  random bytes, and both Syncthing identities are derived from it: the machine uses
  `seed + "server"`, the key holder `seed + "client"`. The passphrase itself is never in
  the header; it lives in the QR code and on the phone.
- What seed disclosure costs you depends on the role, and it is worse for the phone flow:
  - `UNLOCK_ROLE=server` (phone dials out): whoever reads the header can derive the
    **machine's** identity and impersonate it. The phone checks the peer's Device ID, and
    an impersonator presenting that same derived identity passes that check, so the next
    time you tap Unlock the passphrase could go to the attacker rather than your machine.
  - `UNLOCK_ROLE=client` (machine dials out): the reader instead obtains an identity your
    key holder authorises, so they can **ask** for the passphrase. Pinning the key holder's
    Device ID does not prevent that; it only stops them impersonating the key holder.
- This is inherent rather than a flaw in the derivation: any identity a machine can
  reconstruct unattended at boot is available to whoever holds its disk, exactly as a
  Clevis header plus reachable Tang server unlocks a stolen disk. Sealing the key to a TPM
  is what changes it.
- Practical mitigations: `syncthing-socket server --totp`, running the key holder only when
  you actually intend to unlock, and treating a disk that has left your control as needing
  re-enrolment with a fresh seed.
- The passphrase never touches disk on the booting machine. It goes from the client's stdout
  through a FIFO in `/run` straight into cryptsetup.
- Traffic is TLS 1.3 end to end and relays cannot read it, but they do see that two device
  IDs are talking.
