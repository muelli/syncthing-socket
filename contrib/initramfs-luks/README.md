# syncthing-socket LUKS Decryption

Use `syncthing-socket` to automatically decrypt your LUKS root drive during an Ubuntu server boot. The server will request the secret over the Syncthing network, and you can provide it on-demand from your laptop.

This integration uses the `--passphrase` feature to deterministically generate ephemeral TLS certificates for the boot session, meaning no static certificates need to be permanently embedded into your unencrypted `/boot` partition.

## How it works

- **Server (During Boot):** Runs `syncthing-socket client` inside an `initramfs` loop with exponential backoff. The output of this command is piped directly into `cryptsetup`.
- **Laptop (On-Demand):** You run a `syncthing-socket server` command and pipe your symmetric LUKS key into it.

The keyscript gracefully degrades and runs in parallel with standard `/lib/cryptsetup/askpass`. This means it works concurrently with Clevis (Tang/TPM) systemd agents or manual keyboard entry! Whichever method provides the key first unlocks the drive immediately.

## Installation & Configuration

### 1. Configure the boot process

1. Place `syncthing-socket-hook` into `/etc/initramfs-tools/hooks/syncthing-socket` and `chmod +x` it.
2. Place `syncthing-socket-keyscript` into `/lib/cryptsetup/scripts/syncthing-socket-keyscript` and `chmod +x` it.
3. Configure the script using one of the following methods:
   - **Option A (Script Config):** Hardcode your `P2P_KEY_SEED` and `KEY_BEARING_DEVICE_ID` at the top of the `syncthing-socket-keyscript` file.
   - **Option B (LUKS Token Config):** Run the bind tool on your drive to inject the configuration natively into the LUKS header: 
     `./syncthing-luks-bind /dev/nvme0n1p3 "your-key-seed" "KEY-BEARING-DEVICE-ID"`
4. Update `/etc/crypttab` to use the keyscript by appending `,keyscript=/lib/cryptsetup/scripts/syncthing-socket-keyscript` to the drive options.
   Example: `target_name UUID=... none luks,keyscript=/lib/cryptsetup/scripts/syncthing-socket-keyscript`
5. Ensure the initramfs network is enabled (e.g., add `IP=dhcp` in `/etc/initramfs-tools/initramfs.conf`).
6. Run `update-initramfs -u`.

### 2. Unlock remotely

When the server boots, it will reach out to the Syncthing global discovery network. From your laptop, trigger the unlock by running:

```bash
read -s -p "LUKS Password: " LUKS_PASS && echo -n "$LUKS_PASS" | syncthing-socket server --passphrase "your-key-seed"
```
