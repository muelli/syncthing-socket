# Systemd Service Configuration

This directory contains a systemd service file to run `syncthing-socket` persistently in the background on the SSH host.

## Files
- `syncthing-ssh-forwarder.service`: Systemd service unit.

## Instructions

### 1. Build and Install the Binary
Compile the binary on the host machine and install it to `/usr/local/bin/`:

```bash
go build -o syncthing-socket main.go
sudo cp syncthing-socket /usr/local/bin/
```

### 2. Install the Service File
Copy the service file to the systemd directory:

```bash
sudo cp contrib/syncthing-ssh-forwarder.service /etc/systemd/system/
```

### 3. Start and Enable the Service
Reload systemd, start the service, and configure it to launch automatically at boot:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now syncthing-ssh-forwarder.service
```

### 4. Retrieve the Server Device ID
The service runs in a secure sandbox using `DynamicUser=yes`. Its persistent data (certificates) are stored in `/var/lib/syncthing-ssh-forwarder/`, managed automatically by systemd.

To retrieve the Server Device ID and connection command, read the service logs:

```bash
journalctl -u syncthing-ssh-forwarder.service
```

Look for the log output containing:
```text
==================================================
Server Device ID: <SERVER_DEVICE_ID>
==================================================
Connected to Relay: relay://...
To connect, run:
  ./syncthing-socket client ...
==================================================
```
