#!/bin/bash
# Stop the password agent before switching to the real root.
#
# The agent loops forever by design, so without this it survives the switch as an orphan
# holding the console and a network connection it has no further use for. Its transfer
# subprocess, if one is running, is killed by the kernel: the agent sets PDEATHSIG on it
# rather than relying on a "ps" that dracut does not guarantee is present.

# shellcheck disable=SC1091
type getarg > /dev/null 2>&1 || . /lib/dracut-lib.sh

[ -s /run/syncthing-socket-luks.pid ] || return 0

read -r agent_pid < /run/syncthing-socket-luks.pid
[ -n "$agent_pid" ] && kill -TERM "$agent_pid" 2> /dev/null
rm -f /run/syncthing-socket-luks.pid
