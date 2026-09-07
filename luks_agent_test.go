package socket

import (
	"strings"
	"testing"
)

// A real request, as systemd writes it. Keeping a verbatim sample matters: the parser is
// the one thing standing between a boot prompt and an unanswered one, and the format is
// somebody else's to change.
const sampleAskFile = `[Ask]
PID=412
Socket=/run/systemd/ask-password/sck.a1b2c3d4e5f6
AcceptCached=1
Echo=0
NotAfter=91250000
Message=Please enter passphrase for disk cryptroot
Id=cryptsetup:/dev/vda1
`

func TestParseAskFile(t *testing.T) {
	req, err := parseAskFile(strings.NewReader(sampleAskFile))
	if err != nil {
		t.Fatalf("parseAskFile: %v", err)
	}
	if want := "/run/systemd/ask-password/sck.a1b2c3d4e5f6"; req.socket != want {
		t.Errorf("socket = %q, want %q", req.socket, want)
	}
	if want := "cryptsetup:/dev/vda1"; req.id != want {
		t.Errorf("id = %q, want %q", req.id, want)
	}
	if req.notAfter != 91250000 {
		t.Errorf("notAfter = %d, want 91250000", req.notAfter)
	}
	if !req.wantsCryptsetup() {
		t.Error("a cryptsetup request was not recognised as one")
	}
}

// A request with no socket cannot be answered, so it must be an error rather than a
// silently useless request that the loop retries forever.
func TestParseAskFileWithoutSocket(t *testing.T) {
	if _, err := parseAskFile(strings.NewReader("[Ask]\nId=cryptsetup:/dev/vda1\n")); err == nil {
		t.Fatal("a request with no Socket= was accepted")
	}
}

// The dangerous failure is reading a malformed NotAfter as zero, which means "never
// expires": the agent would then keep answering a request systemd has long abandoned.
func TestParseAskFileRejectsMalformedNotAfter(t *testing.T) {
	broken := strings.Replace(sampleAskFile, "NotAfter=91250000", "NotAfter=soon", 1)
	if _, err := parseAskFile(strings.NewReader(broken)); err == nil {
		t.Fatal("an unparseable NotAfter was accepted, which would read as never expiring")
	}
}

func TestExpiry(t *testing.T) {
	req := &askRequest{notAfter: 1000}
	if req.expired(999) {
		t.Error("a request expired one microsecond early")
	}
	if !req.expired(1000) {
		t.Error("a request did not expire at its own deadline")
	}
	// NotAfter is optional; absent means it never expires.
	forever := &askRequest{notAfter: 0}
	if forever.expired(1 << 62) {
		t.Error("a request with no NotAfter expired")
	}
}

// Handing a LUKS passphrase to whatever else happens to be asking would be its own kind
// of bug, so anything that is not cryptsetup must be ignored.
func TestOnlyCryptsetupRequestsAreAnswered(t *testing.T) {
	for _, id := range []string{"", "ssh-keygen", "cryptenroll", "unknown"} {
		req := &askRequest{id: id}
		if req.wantsCryptsetup() {
			t.Errorf("id %q was treated as a cryptsetup request", id)
		}
	}
	for _, id := range []string{"cryptsetup", "cryptsetup:/dev/vda1"} {
		req := &askRequest{id: id}
		if !req.wantsCryptsetup() {
			t.Errorf("id %q was not treated as a cryptsetup request", id)
		}
	}
}

func TestParseLUKSToken(t *testing.T) {
	ours := []byte(`{"type":"syncthing-socket","keyslots":["0"],` +
		`"p2p_key_seed":"c2VlZA==","key_bearing_device_id":"AAAA-BBBB","unlock_role":"server"}`)
	cfg, err := parseLUKSToken(ours)
	if err != nil {
		t.Fatalf("parseLUKSToken: %v", err)
	}
	if cfg == nil {
		t.Fatal("our own token was not recognised")
	}
	if cfg.Seed != "c2VlZA==" || cfg.KeyBearingDeviceID != "AAAA-BBBB" || cfg.Role != "server" {
		t.Errorf("token parsed as %+v", cfg)
	}
}

// A header may hold Clevis or systemd-tpm2 tokens alongside ours. Those are not an error
// and not ours; the scan must simply move on.
func TestParseLUKSTokenIgnoresOtherTypes(t *testing.T) {
	cfg, err := parseLUKSToken([]byte(`{"type":"clevis","keyslots":["1"]}`))
	if err != nil {
		t.Fatalf("a foreign token was an error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("a clevis token was claimed as ours: %+v", cfg)
	}
}

func TestParseLUKSTokenRejectsUnusable(t *testing.T) {
	for name, raw := range map[string]string{
		"no seed":  `{"type":"syncthing-socket","key_bearing_device_id":"X"}`,
		"bad role": `{"type":"syncthing-socket","p2p_key_seed":"s","unlock_role":"sideways"}`,
		"garbage":  `{"type":`,
	} {
		if _, err := parseLUKSToken([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The role defaults to client, matching syncthing-luks-bind's own default.
func TestParseLUKSTokenDefaultsToClient(t *testing.T) {
	cfg, err := parseLUKSToken([]byte(`{"type":"syncthing-socket","p2p_key_seed":"s"}`))
	if err != nil {
		t.Fatalf("parseLUKSToken: %v", err)
	}
	if cfg.Role != "client" {
		t.Errorf("role = %q, want client", cfg.Role)
	}
}

// luks.conf is sourced by /bin/sh on the initramfs-tools side, so values are frequently
// quoted. Reading the quotes as part of the seed would produce a valid-looking config
// that derives the wrong identity and fails at boot with nothing to point at.
func TestParseLuksConfStripsQuotes(t *testing.T) {
	conf := `# the unlock configuration
P2P_KEY_SEED="c2VlZA=="
KEY_BEARING_DEVICE_ID='AAAA-BBBB'
UNLOCK_ROLE=server
`
	cfg, err := parseLuksConf(strings.NewReader(conf))
	if err != nil {
		t.Fatalf("parseLuksConf: %v", err)
	}
	if cfg == nil {
		t.Fatal("a configured luks.conf was read as empty")
	}
	if cfg.Seed != "c2VlZA==" {
		t.Errorf("seed = %q, want c2VlZA==", cfg.Seed)
	}
	if cfg.KeyBearingDeviceID != "AAAA-BBBB" {
		t.Errorf("device id = %q, want AAAA-BBBB", cfg.KeyBearingDeviceID)
	}
	if cfg.Role != "server" {
		t.Errorf("role = %q, want server", cfg.Role)
	}
}

// A luks.conf with no seed must fall through to the LUKS2 header rather than counting as
// a configuration.
func TestParseLuksConfWithoutSeed(t *testing.T) {
	cfg, err := parseLuksConf(strings.NewReader("UNLOCK_ROLE=server\n"))
	if err != nil {
		t.Fatalf("parseLuksConf: %v", err)
	}
	if cfg != nil {
		t.Fatalf("a seedless luks.conf was treated as configured: %+v", cfg)
	}
}

const sampleLuksDump = `LUKS header information
Version:       	2
Epoch:         	5
Metadata area: 	16384 [bytes]
Keyslots:
  0: luks2
	Key:        512 bits
Tokens:
  0: clevis
  2: syncthing-socket
Digests:
  0: pbkdf2
`

// The token id is whatever cryptsetup assigned. Assuming 0 breaks as soon as Clevis or
// systemd-tpm2 has a token in the same header, which is exactly the case this covers.
func TestTokenIDsFromDump(t *testing.T) {
	ids := tokenIDsFromDump(sampleLuksDump, "syncthing-socket")
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("ids = %v, want [2]", ids)
	}
	if got := tokenIDsFromDump(sampleLuksDump, "clevis"); len(got) != 1 || got[0] != 0 {
		t.Errorf("clevis ids = %v, want [0]", got)
	}
	// The Digests section must not be read as more tokens.
	if got := tokenIDsFromDump(sampleLuksDump, "pbkdf2"); len(got) != 0 {
		t.Errorf("a Digests entry was read as a token: %v", got)
	}
}

func TestTokenIDsFromDumpWithoutTokens(t *testing.T) {
	if got := tokenIDsFromDump("LUKS header information\nVersion: 2\n", "syncthing-socket"); len(got) != 0 {
		t.Errorf("ids = %v, want none", got)
	}
}

func TestBlockDevices(t *testing.T) {
	const procPartitions = `major minor  #blocks  name

 253        0   20971520 vda
 253        1   20970496 vda1
   1        0      65536 ram0
   7        0     123456 loop0
  11        0    1048575 sr0
`
	got := blockDevices(strings.NewReader(procPartitions))
	want := []string{"/dev/vda", "/dev/vda1"}
	if len(got) != len(want) {
		t.Fatalf("devices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("devices = %v, want %v", got, want)
		}
	}
}

// fakeRunner stands in for cryptsetup so the device scan can be exercised without a LUKS
// device or root.
type fakeRunner struct {
	dumps  map[string]string
	tokens map[string]string
	calls  int
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	f.calls++
	if len(args) > 0 && args[0] == "luksDump" {
		dump, ok := f.dumps[args[len(args)-1]]
		if !ok {
			return nil, errNotLUKS
		}
		return []byte(dump), nil
	}
	tok, ok := f.tokens[args[len(args)-1]+":"+args[3]]
	if !ok {
		return nil, errNotLUKS
	}
	return []byte(tok), nil
}

var errNotLUKS = &runError{}

type runError struct{}

func (*runError) Error() string { return "exit status 1" }

// The whole scan, end to end: skip a device that is not LUKS, skip a token belonging to
// somebody else, and come back with ours plus the device it was found on.
func TestFindLUKSConfigScansDevices(t *testing.T) {
	runner := &fakeRunner{
		dumps: map[string]string{
			"/dev/vdb": sampleLuksDump,
		},
		tokens: map[string]string{
			"/dev/vdb:0": `{"type":"clevis","keyslots":["1"]}`,
			"/dev/vdb:2": `{"type":"syncthing-socket","p2p_key_seed":"c2VlZA==",` +
				`"key_bearing_device_id":"AAAA-BBBB","unlock_role":"server"}`,
		},
	}

	cfg, err := scanDevicesForConfig(runner, []string{"/dev/vda", "/dev/vdb"})
	if err != nil {
		t.Fatalf("scanDevicesForConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("the token was not found")
	}
	if cfg.Device != "/dev/vdb" {
		t.Errorf("device = %q, want /dev/vdb", cfg.Device)
	}
	if cfg.Seed != "c2VlZA==" || cfg.Role != "server" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestFindLUKSConfigFindsNothing(t *testing.T) {
	runner := &fakeRunner{dumps: map[string]string{}}
	cfg, err := scanDevicesForConfig(runner, []string{"/dev/vda"})
	if err != nil {
		t.Fatalf("scanDevicesForConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("a config appeared from nowhere: %+v", cfg)
	}
}

// A verbatim /run/systemd/netif/state from the 26.04 test VM. On a dracut initrd without
// systemd-resolved, which is what Ubuntu 26.04 builds, this is the only place the
// nameserver appears; nothing writes a resolv.conf at all.
const sampleNetworkdState = `# This is private data. Do not parse.
OPER_STATE=routable
CARRIER_STATE=carrier
ADDRESS_STATE=routable
IPV4_ADDRESS_STATE=routable
IPV6_ADDRESS_STATE=degraded
ONLINE_STATE=online
DNS=192.168.118.1
NTP=192.168.118.1
DOMAINS=fritz.box
`

func TestDNSFromNetworkdState(t *testing.T) {
	got := dnsFromNetworkdState(strings.NewReader(sampleNetworkdState))
	if len(got) != 1 || got[0] != "192.168.118.1" {
		t.Fatalf("servers = %v, want [192.168.118.1]", got)
	}
}

// networkd writes several servers on one line, space separated.
func TestDNSFromNetworkdStateWithSeveralServers(t *testing.T) {
	got := dnsFromNetworkdState(strings.NewReader("DNS=192.0.2.1 192.0.2.2 2001:db8::1\n"))
	want := []string{"192.0.2.1", "192.0.2.2", "2001:db8::1"}
	if len(got) != len(want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("servers = %v, want %v", got, want)
		}
	}
}

// The format carries a "do not parse" warning, so the failure mode has to be an empty
// list rather than nonsense written into resolv.conf.
func TestDNSFromNetworkdStateWithoutDNS(t *testing.T) {
	for _, input := range []string{
		"# This is private data. Do not parse.\nOPER_STATE=routable\n",
		"",
		"NTP=192.0.2.1\nDOMAINS=example.org\n",
		"DNS=\n",
	} {
		if got := dnsFromNetworkdState(strings.NewReader(input)); len(got) != 0 {
			t.Errorf("input %q gave %v, want nothing", input, got)
		}
	}
}

// A DNS= that is a prefix of another key must not match.
func TestDNSFromNetworkdStateIgnoresSimilarKeys(t *testing.T) {
	if got := dnsFromNetworkdState(strings.NewReader("DNSSEC=no\nDNS_OVER_TLS=no\n")); len(got) != 0 {
		t.Errorf("got %v from DNSSEC and DNS_OVER_TLS, want nothing", got)
	}
}

func TestGlobAll(t *testing.T) {
	if got := globAll("/run/systemd/netif/state"); len(got) != 1 || got[0] != "/run/systemd/netif/state" {
		t.Errorf("a plain path was not passed through: %v", got)
	}
	// A pattern matching nothing must yield nothing rather than the pattern itself,
	// which would then be opened as a literal filename.
	if got := globAll("/nonexistent-dir-for-tests/*.conf"); len(got) != 0 {
		t.Errorf("an unmatched pattern gave %v, want nothing", got)
	}
}
