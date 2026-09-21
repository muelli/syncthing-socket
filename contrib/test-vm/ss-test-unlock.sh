#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Boot the VM and check how it gets past its LUKS prompt.
#
# MODE=relay    a key holder is listening; the machine should unlock itself.
# MODE=console  nobody is listening; the passphrase is typed at the prompt instead, which
#               is the property that distinguishes this from a keyscript and must keep
#               working while the agent is actively retrying.
set -euo pipefail

OUTDIR="${OUTDIR:?set OUTDIR}"
MODE="${MODE:-relay}"
SEED="${SEED:?set SEED}"
BINARY="${BINARY:?set BINARY}"
VM_PASSPHRASE="${VM_PASSPHRASE:-passphrase}"
TIMEOUT="${TIMEOUT:-300}"
HERE="$(cd "$(dirname "$0")" && pwd)"
LOG="$OUTDIR/console-$MODE.log"
: > "$LOG"

cleanup() {
	set +e
	[ -n "${HOLDER_PID:-}" ] && kill "$HOLDER_PID" 2>/dev/null
	[ -n "${QEMU_PID:-}" ] && kill "$QEMU_PID" 2>/dev/null
	pkill -f "keyholder-$OUTDIR" 2>/dev/null
	return 0
}
trap cleanup EXIT

if [ "$MODE" = relay ]; then
	VM_CLIENT_ID=$("$BINARY" id --passphrase "$SEED" | awk '/^Client ID:/{print $3}')
	cat > "$OUTDIR/keyholder.sh" <<EOF
#!/bin/bash
# keyholder-$OUTDIR
while true; do
  printf %s '$VM_PASSPHRASE' | $BINARY server --passphrase '$SEED' \\
    --authorized-clients '$VM_CLIENT_ID' --announce-interval 60s --log-level info
  echo "--- key holder served or exited, restarting \$(date -Is)"
  sleep 2
done
EOF
	chmod +x "$OUTDIR/keyholder.sh"
	nohup "$OUTDIR/keyholder.sh" > "$OUTDIR/keyholder.log" 2>&1 &
	HOLDER_PID=$!
	echo "key holder started (pid $HOLDER_PID); waiting for it to announce"
	for i in $(seq 1 20); do
		sleep 5
		grep -q "Successfully announced" "$OUTDIR/keyholder.log" && break
	done
	grep -c "Successfully announced" "$OUTDIR/keyholder.log" || true
fi

echo "booting"
nohup "$HERE/ss-run-vm.sh" > "$OUTDIR/qemu.log" 2>&1 &
QEMU_PID=$!

if [ "$MODE" = console ]; then
	# Type the passphrase when the prompt appears, with the agent running and failing.
	# Wait for the agent to have tried and failed at least once before typing, not
	# merely for the prompt. The prompt appears seconds before the agent starts, so
	# matching on it proves only that console entry works on a machine where the agent
	# is not running yet. The property worth testing is that an agent actively retrying
	# does not block a human typing the passphrase, which is what separates this from a
	# keyscript.
	python3 "$HERE/ss-console.py" --socket "$OUTDIR/console.sock" --log "$LOG" \
		--duration "$TIMEOUT" \
		--send-on 'no passphrase yet|does not unlock|rejected the passphrase' \
		--send "$VM_PASSPHRASE" \
		--stop-on 'Welcome to Ubuntu|login:' || true
else
	python3 "$HERE/ss-console.py" --socket "$OUTDIR/console.sock" --log "$LOG" \
		--duration "$TIMEOUT" \
		--stop-on 'Welcome to Ubuntu|login:' || true
fi

echo
echo "########## RESULT ($MODE) ##########"
if grep -qE "Welcome to Ubuntu|login:" "$LOG"; then echo "BOOTED: yes"; else echo "BOOTED: no"; fi
echo "--- syncthing-socket lines from the console:"
sed 's/\x1b\[[0-9;]*m//g' "$LOG" | tr -d '\r' | grep -iE "syncthing-socket:|does not unlock|no passphrase|rejected|resolver" | head -20
echo "--- prompt and unlock milestones:"
sed 's/\x1b\[[0-9;]*m//g' "$LOG" | tr -d '\r' | grep -iE "Please enter passphrase|Please unlock disk|Finished .*cryptsetup|cryptsetup: .*set up successfully|Welcome to Ubuntu|ALERT!" | head -10
echo "########## END ##########"
