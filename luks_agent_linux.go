//go:build linux

package socket

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// monotonicUsec returns CLOCK_MONOTONIC in microseconds, the clock systemd's NotAfter
// field is expressed in. Wall clock would be wrong: an initrd's idea of the date is
// frequently nonsense before the RTC is read.
func monotonicUsec() (uint64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, err
	}
	return uint64(ts.Sec)*1000000 + uint64(ts.Nsec)/1000, nil
}

// replyToAsk sends a password to the socket named in a request.
//
// The socket is an AF_UNIX datagram socket and the payload is "+" followed by the
// password, per systemd's password agent protocol. Raw syscalls rather than net.Dial
// because an unbound datagram socket is exactly what is wanted here: systemd reads the
// sender's credentials from the kernel and never replies, so binding a local address
// would only create a file in an initramfs that nothing cleans up.
func replyToAsk(socketPath, password string) error {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("cannot create a reply socket: %w", err)
	}
	defer unix.Close(fd)

	if err := unix.Sendto(fd, []byte("+"+password), 0, &unix.SockaddrUnix{Name: socketPath}); err != nil {
		return fmt.Errorf("cannot send to %s: %w", socketPath, err)
	}
	return nil
}

// runLUKSAgent watches for password requests and answers the ones meant for us. It never
// returns on its own; the initrd kills it before switching root.
func runLUKSAgent() error {
	runner := execRunner{}

	// Requests are identified by their file path, which systemd makes unique per
	// request. That is what makes a rejected passphrase retryable: cryptsetup asks
	// again with a new file, and a new file is one we have not answered. Keying this on
	// the device instead was a real bug on the initramfs-tools side, where one wrong
	// passphrase silenced the network path for the rest of the boot.
	answered := map[string]bool{}
	// Whether we have already handed over a passphrase this run. cryptsetup only asks
	// again when the key it got was refused, so a fresh request after we answered one is
	// a rejection and should say so. Left implicit, the console shows "delivered"
	// followed by another prompt and leaves the reader to infer it.
	delivered := false

	slog.Info("waiting for a cryptsetup password request")

	for {
		time.Sleep(agentPollInterval)

		requests, err := pendingRequests(answered)
		if err != nil || len(requests) == 0 {
			continue
		}

		cfg, err := findLUKSConfig(runner)
		if err != nil {
			slog.Error("cannot read the unlock configuration", "error", err)
			time.Sleep(agentRetryInterval)
			continue
		}
		if cfg == nil {
			// Nothing configured on this machine. Leave every prompt to the
			// console; answering is not ours to do.
			for _, req := range requests {
				answered[req.path] = true
			}
			continue
		}

		for _, req := range requests {
			// The network is usually not up yet when this agent starts, so this has
			// to be re-checked rather than settled once.
			ensureResolver()

			if delivered {
				fmt.Fprintf(os.Stderr,
					"syncthing-socket: %s rejected the passphrase, asking again\n",
					cfg.Device)
			}
			fmt.Fprintf(os.Stderr, "syncthing-socket: requesting the passphrase for %s\n", cfg.Device)

			passphrase, err := fetchPassphrase(cfg)
			if err != nil || passphrase == "" {
				// Usually the key holder is simply not listening yet.
				slog.Warn("no passphrase yet, retrying", "error", err)
				time.Sleep(agentRetryInterval)
				continue
			}

			// Do not spend cryptsetup's prompt on a key we can already tell is wrong.
			if err := verifyPassphrase(cfg.Device, passphrase); err != nil {
				// The length is the one detail that distinguishes the interesting
				// cases, a truncated transfer from a genuinely different passphrase,
				// and it is only printed when something is already wrong.
				fmt.Fprintf(os.Stderr,
					"syncthing-socket: the passphrase received (%d bytes) does not "+
						"unlock %s; not using it\n", len(passphrase), cfg.Device)
				slog.Warn("received a passphrase that does not unlock the device",
					"device", cfg.Device, "bytes", len(passphrase))
				time.Sleep(agentRetryInterval)
				continue
			}

			if err := replyToAsk(req.socket, passphrase); err != nil {
				slog.Error("cannot deliver the passphrase", "error", err)
				continue
			}
			answered[req.path] = true
			delivered = true
			fmt.Fprintf(os.Stderr, "syncthing-socket: passphrase delivered for %s\n", cfg.Device)
		}
	}
}

// pendingRequests returns the unanswered, unexpired cryptsetup requests.
func pendingRequests(answered map[string]bool) ([]*askRequest, error) {
	entries, err := os.ReadDir(askPasswordDir)
	if err != nil {
		// The directory only exists once systemd has something to ask about.
		return nil, err
	}

	now, err := monotonicUsec()
	if err != nil {
		return nil, err
	}

	var requests []*askRequest
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "ask.") {
			continue
		}
		path := filepath.Join(askPasswordDir, entry.Name())
		if answered[path] {
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			continue
		}
		req, err := parseAskFile(f)
		f.Close()
		if err != nil {
			slog.Warn("ignoring an unreadable password request", "path", path, "error", err)
			answered[path] = true
			continue
		}
		req.path = path

		if req.expired(now) {
			answered[path] = true
			continue
		}
		if !req.wantsCryptsetup() {
			// Somebody else's prompt. Never hand a LUKS passphrase to it.
			answered[path] = true
			continue
		}
		requests = append(requests, req)
	}

	// Forget requests whose files are gone, so a long boot cannot grow this without
	// bound.
	for path := range answered {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			delete(answered, path)
		}
	}

	return requests, nil
}

// setChildProcAttrs asks the kernel to kill the transfer subprocess if the agent itself
// dies. The initrd kills the agent before switching to the real root, and without this
// the child survives as an orphan holding the console and a relay connection.
func setChildProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
