#!/bin/bash
# Start the password agent, in the background, so it is already polling while
# systemd-cryptsetup waits for an answer.
#
# It must not block: this hook runs before the root filesystem is mounted, and anything
# that waits here waits forever if no key holder ever appears. The agent answers if it
# can, the console prompt stays live either way.

# shellcheck disable=SC1091
type getarg > /dev/null 2>&1 || . /lib/dracut-lib.sh

command -v syncthing-socket > /dev/null 2>&1 || return 0

# rd.syncthing_socket=0 turns the whole thing off from the kernel command line, which is
# the escape hatch when the network path is what is broken and you just want the prompt.
if ! getargbool 1 rd.syncthing_socket; then
    info "syncthing-socket: disabled by rd.syncthing_socket=0"
    return 0
fi

# The initqueue can run a hook more than once, and one agent is enough.
[ -e /run/syncthing-socket-luks.pid ] && return 0

# Setting up a resolver is deliberately left to the agent. This hook runs when udev has
# settled, which is before the network is up, so anything decided here would be decided at
# the one moment when the answer is always "no resolver yet".

syncthing-socket luks-agent > /dev/console 2>&1 &
echo $! > /run/syncthing-socket-luks.pid
info "syncthing-socket: password agent started"
