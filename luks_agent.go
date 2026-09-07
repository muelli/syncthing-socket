package socket

// A systemd password agent, so a dracut initrd can be unlocked the same way an
// initramfs-tools one is.
//
// The initramfs-tools integration races cryptsetup's own askpass: it finds the running
// /lib/cryptsetup/askpass, borrows the passfifo it reads from, and writes the passphrase
// in. Neither exists under dracut, which uses systemd-cryptsetup and asks through
// systemd's password agent protocol instead.
//
// That protocol is the exact analogue, and it is the reason this is not a keyscript:
// several agents may answer the same request, the first useful answer wins, and the
// console prompt stays live throughout. Manual entry, recovery keys and TPM agents all
// keep working, and nothing is lost if we never answer at all.
//
// Protocol, as documented at https://systemd.io/PASSWORD_AGENTS/: requests appear as
// /run/systemd/ask-password/ask.XXXXXX, an ini file naming a datagram socket to answer
// on. A reply is "+" followed by the password, or "-" to decline.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Where systemd puts password requests.
	askPasswordDir = "/run/systemd/ask-password"

	// Only answer requests that come from cryptsetup. systemd uses the same mechanism
	// for ssh keys and other unrelated prompts, and handing a LUKS passphrase to
	// whatever happens to be asking would be its own kind of bug.
	cryptsetupIDPrefix = "cryptsetup"

	// The directory changes maybe twice in a boot, so polling is plenty and saves an
	// inotify dependency in an initramfs.
	agentPollInterval = 1 * time.Second

	// How long to wait before looking for work again after a failed unlock. A fresh
	// announcement takes 30 to 45 seconds to reach global discovery, so the key holder
	// usually just is not listening yet.
	agentRetryInterval = 5 * time.Second
)

// askRequest is one entry in the ask-password directory.
type askRequest struct {
	path     string
	socket   string
	id       string
	message  string
	notAfter uint64 // CLOCK_MONOTONIC microseconds; 0 means it never expires
}

// parseAskFile reads systemd's ini-ish ask.XXXXXX format. It is not a full ini parser on
// purpose: the file has one section and systemd writes it, so the only things worth
// handling are comments, the section header and key=value.
func parseAskFile(r io.Reader) (*askRequest, error) {
	req := &askRequest{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Socket":
			req.socket = strings.TrimSpace(value)
		case "Id":
			req.id = strings.TrimSpace(value)
		case "Message":
			req.message = strings.TrimSpace(value)
		case "NotAfter":
			// A malformed timestamp must not be read as "never expires", so treat
			// anything unparseable as already expired rather than guessing.
			n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unparseable NotAfter %q: %w", value, err)
			}
			req.notAfter = n
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if req.socket == "" {
		return nil, fmt.Errorf("no Socket= in the request")
	}
	return req, nil
}

// expired reports whether systemd has already given up on this request. Answering an
// expired one is harmless but pointless, and the socket is usually gone by then.
func (a *askRequest) expired(nowUsec uint64) bool {
	return a.notAfter != 0 && nowUsec >= a.notAfter
}

// wantsCryptsetup reports whether this request is one we should answer at all.
func (a *askRequest) wantsCryptsetup() bool {
	return strings.HasPrefix(a.id, cryptsetupIDPrefix)
}

// luksConfig is the unlock configuration, from the LUKS2 header or from luks.conf. It
// mirrors what contrib/initramfs-luks/syncthing-socket-initramfs-top reads.
type luksConfig struct {
	Device             string
	Seed               string `json:"p2p_key_seed"`
	KeyBearingDeviceID string `json:"key_bearing_device_id"`
	Role               string `json:"unlock_role"`
}

// luksToken is the shape syncthing-luks-bind writes into the header.
type luksToken struct {
	Type               string `json:"type"`
	Seed               string `json:"p2p_key_seed"`
	KeyBearingDeviceID string `json:"key_bearing_device_id"`
	Role               string `json:"unlock_role"`
}

const luksTokenType = "syncthing-socket"

// parseLUKSToken pulls our configuration out of one `cryptsetup token export` document,
// returning nil for a token belonging to somebody else (Clevis, systemd-tpm2).
func parseLUKSToken(raw []byte) (*luksConfig, error) {
	var tok luksToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	if tok.Type != luksTokenType {
		return nil, nil
	}
	if tok.Seed == "" {
		return nil, fmt.Errorf("the %s token carries no p2p_key_seed", luksTokenType)
	}
	role := tok.Role
	if role == "" {
		role = "client"
	}
	if role != "client" && role != "server" {
		return nil, fmt.Errorf("unknown unlock_role %q", role)
	}
	return &luksConfig{
		Seed:               tok.Seed,
		KeyBearingDeviceID: tok.KeyBearingDeviceID,
		Role:               role,
	}, nil
}

// parseLuksConf reads /etc/syncthing-socket/luks.conf, the shell-sourced override that
// takes precedence over the header. Same file the initramfs-tools script sources.
func parseLuksConf(r io.Reader) (*luksConfig, error) {
	cfg := &luksConfig{Role: "client"}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		// It is a shell fragment, so the value may be quoted.
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "P2P_KEY_SEED":
			cfg.Seed = value
		case "KEY_BEARING_DEVICE_ID":
			cfg.KeyBearingDeviceID = value
		case "UNLOCK_ROLE":
			if value != "" {
				cfg.Role = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if cfg.Seed == "" {
		return nil, nil
	}
	if cfg.Role != "client" && cfg.Role != "server" {
		return nil, fmt.Errorf("unknown UNLOCK_ROLE %q", cfg.Role)
	}
	return cfg, nil
}

// tokenIDsFromDump scrapes the token ids out of `cryptsetup luksDump`. Doing it this way
// costs one exec per device instead of thirty-two, and it names the type so we can skip
// devices that carry only somebody else's tokens.
//
// The section looks like:
//
//	Tokens:
//	  0: clevis
//	  1: syncthing-socket
func tokenIDsFromDump(dump string, tokenType string) []int {
	var ids []int
	inTokens := false
	for _, line := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			// A new unindented heading ends the Tokens section.
			inTokens = strings.HasPrefix(line, "Tokens:")
			continue
		}
		if !inTokens {
			continue
		}
		idPart, typePart, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		if strings.TrimSpace(typePart) != tokenType {
			continue
		}
		if id, err := strconv.Atoi(strings.TrimSpace(idPart)); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// blockDevices lists candidate whole devices and partitions from /proc/partitions, which
// needs no lsblk and no udev. Reading /sys would work too but this is one file.
func blockDevices(procPartitions io.Reader) []string {
	var devices []string
	scanner := bufio.NewScanner(procPartitions)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 || fields[0] == "major" {
			continue
		}
		name := fields[3]
		// Skip things that are never a LUKS container in an initrd, to keep the
		// number of cryptsetup execs down.
		if strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "loop") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "sr") {
			continue
		}
		devices = append(devices, "/dev/"+name)
	}
	return devices
}

// findLUKSConfig locates the unlock configuration. luks.conf wins if it sets a seed,
// exactly as on initramfs-tools; otherwise every block device is examined for a LUKS2
// header carrying our token.
//
// This is where the dracut port genuinely differs from the initramfs-tools one rather
// than merely being spelled differently. There, askpass inherits CRYPTTAB_SOURCE and the
// device is simply read out of /proc/<pid>/environ. systemd-cryptsetup sets no such
// variable, so the device has to be found rather than told. On a machine with two volumes
// carrying our token the first one found wins; a single encrypted root is the documented
// case.
func findLUKSConfig(runner commandRunner) (*luksConfig, error) {
	if f, err := os.Open(luksConfPath); err == nil {
		cfg, parseErr := parseLuksConf(f)
		f.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		if cfg != nil {
			return cfg, nil
		}
	}

	procPartitions, err := os.Open("/proc/partitions")
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate block devices: %w", err)
	}
	defer procPartitions.Close()

	return scanDevicesForConfig(runner, blockDevices(procPartitions))
}

// scanDevicesForConfig looks for a LUKS2 header carrying our token, in order, and returns
// the first match along with the device it was found on.
func scanDevicesForConfig(runner commandRunner, devices []string) (*luksConfig, error) {
	for _, device := range devices {
		dump, err := runner.run("cryptsetup", "luksDump", device)
		if err != nil {
			// Not LUKS, or not readable. Both are ordinary here.
			continue
		}
		for _, id := range tokenIDsFromDump(string(dump), luksTokenType) {
			raw, err := runner.run("cryptsetup", "token", "export",
				"--token-id", strconv.Itoa(id), device)
			if err != nil {
				continue
			}
			cfg, err := parseLUKSToken(raw)
			if err != nil {
				slog.Warn("ignoring an unusable syncthing-socket token",
					"device", device, "token", id, "error", err)
				continue
			}
			if cfg != nil {
				cfg.Device = device
				return cfg, nil
			}
		}
	}
	return nil, nil
}

const luksConfPath = "/etc/syncthing-socket/luks.conf"

const resolvConf = "/etc/resolv.conf"

// resolverSources are the files dracut's various network modules leave a resolver in.
// Which one appears depends on whether systemd-networkd, NetworkManager or the legacy
// dhclient path is in the initrd, so try all of them.
var resolverSources = []string{
	"/run/systemd/resolve/resolv.conf",
	"/run/NetworkManager/resolv.conf",
	"/tmp/net.*.resolv.conf",
}

// networkdStateFiles are where systemd-networkd records what DHCP told it. On a dracut
// initrd without systemd-resolved, which is the Ubuntu 26.04 default, this is the only
// place the nameserver appears at all.
var networkdStateFiles = []string{
	"/run/systemd/netif/state",
	"/run/systemd/netif/leases/*",
}

// dnsFromNetworkdState pulls nameservers out of a systemd-networkd state or lease file.
//
// These files carry a "This is private data. Do not parse." header, and that warning is
// taken seriously: this is a fallback, it is reached only when no resolv.conf-shaped file
// exists anywhere, and if the format ever changes the result is an empty list and the
// same "no resolver" warning the user would have got regardless. Nothing breaks that was
// otherwise working.
func dnsFromNetworkdState(r io.Reader) []string {
	var servers []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		value, found := strings.CutPrefix(line, "DNS=")
		if !found {
			continue
		}
		for _, server := range strings.Fields(value) {
			if server != "" {
				servers = append(servers, server)
			}
		}
	}
	return servers
}

// ensureResolver makes sure there is a resolver before a transfer is attempted.
//
// Without one every discovery lookup fails instantly on [::1]:53 and the unlock never
// gets as far as the network. This is checked on every pass rather than once at startup
// because the agent is started from dracut's "settled" hook, which fires when udev has
// settled and not when the network is up: at that moment there is usually no resolver
// yet, and a one-shot check would conclude there never will be one.
func ensureResolver() {
	if info, err := os.Stat(resolvConf); err == nil && info.Size() > 0 {
		return
	}

	// A file already in resolv.conf shape is the best case; copy it verbatim.
	for _, pattern := range resolverSources {
		for _, candidate := range globAll(pattern) {
			content, err := os.ReadFile(candidate)
			if err != nil || len(content) == 0 {
				continue
			}
			if os.WriteFile(resolvConf, content, 0644) == nil {
				slog.Info("copied a resolver into the initramfs", "from", candidate)
				return
			}
		}
	}

	// Otherwise build one from what DHCP told systemd-networkd.
	for _, pattern := range networkdStateFiles {
		for _, candidate := range globAll(pattern) {
			f, err := os.Open(candidate)
			if err != nil {
				continue
			}
			servers := dnsFromNetworkdState(f)
			f.Close()
			if len(servers) == 0 {
				continue
			}
			var b strings.Builder
			for _, server := range servers {
				fmt.Fprintf(&b, "nameserver %s\n", server)
			}
			if os.WriteFile(resolvConf, []byte(b.String()), 0644) == nil {
				slog.Info("built a resolver from the DHCP lease",
					"from", candidate, "servers", servers)
				return
			}
		}
	}
}

// globAll expands a pattern, treating a plain path as a one-element match so callers can
// mix the two without caring which is which.
func globAll(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

// commandRunner exists so the device scan can be tested without a LUKS device.
type commandRunner interface {
	run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = nil
	return cmd.Output()
}

// fetchPassphrase runs this same binary as a subprocess to do the actual transfer.
//
// Re-executing rather than calling RunClient in process is deliberate. The client and
// server paths reserve file descriptor 1 for the payload and point the logger elsewhere
// (see reservePipeStdout), which is a process-wide change; running it as a child gets
// that behaviour, already tested, without contorting the agent around it.
func fetchPassphrase(cfg *luksConfig) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}

	args := []string{cfg.Role, "--passphrase", cfg.Seed, "--log-level", "error"}
	switch cfg.Role {
	case "server":
		// Announce and wait for the key holder to connect and push the passphrase.
		// Here the Device ID names who we accept rather than who we dial.
		if cfg.KeyBearingDeviceID != "" {
			args = append(args, "--authorized-clients", cfg.KeyBearingDeviceID)
		}
	case "client":
		if cfg.KeyBearingDeviceID != "" {
			args = append(args, cfg.KeyBearingDeviceID)
		}
	default:
		return "", fmt.Errorf("unknown unlock role %q", cfg.Role)
	}

	cmd := exec.Command(self, args...)
	// This machine only ever receives, in both roles.
	cmd.Stdin = nil
	// Keep a copy of what the transfer said as well as showing it on the console.
	// Reporting only "exit status 1" tells whoever is watching a stuck boot nothing at
	// all about whether to wait, check the network, or go and look at the key holder.
	var errBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	// If the agent is killed at the switch to the real root, this child must not outlive
	// it holding the console and a relay connection.
	setChildProcAttrs(cmd)
	out, err := cmd.Output()
	if err != nil {
		if detail := lastLine(errBuf.String()); detail != "" {
			return "", fmt.Errorf("%w: %s", err, detail)
		}
		return "", err
	}
	return string(out), nil
}

// lastLine returns the final non-empty line, which for these subprocesses is the one
// saying what actually went wrong.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
