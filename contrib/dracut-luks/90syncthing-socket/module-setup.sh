#!/bin/bash
# dracut module: fetch a LUKS passphrase during early boot over the Syncthing relay
# network. The dracut counterpart of contrib/initramfs-luks.
#
# INSTALLATION: /usr/lib/dracut/modules.d/90syncthing-socket/
#
# Ubuntu 26.04 and later ship dracut rather than initramfs-tools, and dracut's initrd has
# no /lib/cryptsetup/askpass and no passfifo to race. It uses systemd-cryptsetup, which
# asks through systemd's password agent protocol, so that is what we answer. The design
# goal is unchanged: we do not replace the prompt, we answer alongside it, and whoever
# answers first wins.

# shellcheck disable=SC2154

# Where the binary may be, in the order we prefer. The packages install it in /usr/bin;
# "just install" uses PREFIX=/usr/local and so puts it in /usr/local/bin.
syncthing_socket_path() {
    local candidate
    candidate=$(command -v syncthing-socket 2> /dev/null)
    if [ -n "$candidate" ]; then
        echo "$candidate"
        return 0
    fi
    for candidate in /usr/local/sbin/syncthing-socket /usr/local/bin/syncthing-socket \
        /usr/sbin/syncthing-socket /usr/bin/syncthing-socket; do
        if [ -x "$candidate" ]; then
            echo "$candidate"
            return 0
        fi
    done
    return 1
}

check() {
    # Deliberately not "require_binaries syncthing-socket", which resolves through PATH.
    # An initrd is usually rebuilt from a maintainer script or a kernel hook, and those
    # run with a PATH that excludes /usr/local/bin. A source install lands there, so
    # check() returned 1, dracut omitted the module without saying anything, and the next
    # kernel upgrade quietly produced an initrd with no agent in it. The machine then
    # boots to a console prompt with nothing explaining why.
    syncthing_socket_path > /dev/null || return 1
    return 0
}

depends() {
    # "crypt" brings the LUKS machinery, "network" the stack we need to reach the relay
    # pool. On initramfs-tools both arrive through the cryptsetup-initramfs package
    # dependency; dracut wants them named.
    echo "crypt network"
    return 0
}

install() {
    # Install the copy check() found, and always at the same path inside the initrd, so
    # the hook does not have to search again at boot.
    inst_binary "$(syncthing_socket_path)" /usr/bin/syncthing-socket

    # 70crypt installs the cryptsetup binary only on its non-systemd branch: with systemd
    # present it installs dracut-crypt-generator and relies on systemd-cryptsetup instead.
    # We read the LUKS2 header ourselves, so without this the binary we need is simply
    # absent and the agent fails at run time with nothing obvious to point at.
    inst_multiple cryptsetup

    # Discovery and the relay pool are reached over HTTPS, so the trust store has to come
    # along. Same requirement as the initramfs-tools hook.
    if [ -r /etc/ssl/certs/ca-certificates.crt ]; then
        inst_simple /etc/ssl/certs/ca-certificates.crt
    else
        dwarn "syncthing-socket: /etc/ssl/certs/ca-certificates.crt is missing."
        dwarn "syncthing-socket: install ca-certificates, or discovery will fail at boot."
    fi

    # Optional: the configuration can live here instead of in the LUKS2 header.
    if [ -r /etc/syncthing-socket/luks.conf ]; then
        inst_simple /etc/syncthing-socket/luks.conf
        chmod 0600 "$initdir/etc/syncthing-socket/luks.conf"
    fi

    # The initrd's own default network file is already "DHCP=yes" for every non-loopback
    # interface, but networkd is only waited on when rd.neednet is set. Without this the
    # agent can start before there is a network to use.
    mkdir -p "$initdir/etc/cmdline.d"
    echo "rd.neednet=1" > "$initdir/etc/cmdline.d/95syncthing-socket.conf"

    # The initqueue is the only hook point that runs alongside the password prompt.
    #
    # pre-mount looks like the obvious counterpart to initramfs-tools' local-top, and it
    # is wrong: dracut-pre-mount.service is "After=cryptsetup.target", so it runs only
    # once the volume is already unlocked and the agent could never answer the prompt it
    # exists to answer. That is the same deadlock as declaring PREREQ="cryptroot" on
    # initramfs-tools. dracut-initqueue.service has no such ordering, and "settled" runs
    # once udev has settled, concurrently with systemd-cryptsetup asking.
    #
    # 99 puts us after 99-networkd-run.sh, which is what starts the network.
    inst_hook initqueue/settled 99 "$moddir/syncthing-socket-start.sh"

    # dracut-initqueue.service is conditional on this marker. The network module creates
    # it too, but relying on that would make the unlock depend on which network module
    # happened to be pulled in.
    mkdir -p "$initdir/lib/dracut"
    : > "$initdir/lib/dracut/need-initqueue"
    # pre-pivot corresponds to local-bottom. The agent is an endless loop and must not
    # survive the switch to the real root.
    inst_hook pre-pivot 05 "$moddir/syncthing-socket-stop.sh"
}
