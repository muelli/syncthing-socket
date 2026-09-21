#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Drive a QEMU serial console over a unix socket.

A unix socket rather than QEMU's telnet: transport, because telnet negotiation injects
option bytes into a stream we want to match patterns against, and because only one client
may be attached at a time. A second reader steals typed input, which is how a passphrase
you type can silently vanish.

Logs everything it sees, and can type a line once a pattern appears.
"""
import argparse, os, re, socket, sys, time

parser = argparse.ArgumentParser()
parser.add_argument("--socket", required=True)
parser.add_argument("--log", required=True)
parser.add_argument("--duration", type=float, default=180.0)
parser.add_argument("--send-on", default=None, help="regex; when it matches, send --send")
parser.add_argument("--send", default=None)
parser.add_argument("--stop-on", default=None, help="regex; exit 0 as soon as it matches")
args = parser.parse_args()

deadline = time.time() + args.duration
send_on = re.compile(args.send_on) if args.send_on else None
stop_on = re.compile(args.stop_on) if args.stop_on else None

# The socket may not exist yet if QEMU is still starting.
sock = None
while time.time() < deadline and sock is None:
    try:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.connect(args.socket)
    except (FileNotFoundError, ConnectionRefusedError):
        sock = None
        time.sleep(0.5)
if sock is None:
    print("never managed to attach to the console socket", file=sys.stderr)
    sys.exit(2)

sock.settimeout(1.0)
seen = ""
sent = False
matched = False
with open(args.log, "ab", buffering=0) as log:
    while time.time() < deadline:
        try:
            chunk = sock.recv(4096)
        except socket.timeout:
            continue
        except OSError:
            break
        if not chunk:
            break
        log.write(chunk)
        # Keep a bounded tail: patterns are matched against recent output, and the whole
        # boot is in the log file anyway.
        seen = (seen + chunk.decode("utf-8", "replace"))[-65536:]
        if send_on and not sent and send_on.search(seen):
            time.sleep(0.5)
            sock.sendall((args.send + "\n").encode())
            sent = True
            log.write(b"\n[harness] sent the configured input\n")
        if stop_on and stop_on.search(seen):
            matched = True
            break

print(f"attached={sock is not None} sent={sent} stop_matched={matched}")
sys.exit(0 if (matched or not stop_on) else 1)
