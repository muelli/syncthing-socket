// SPDX-License-Identifier: AGPL-3.0-or-later

package socket

import (
	"crypto/tls"

	"github.com/muelli/syncthing-socket/identity"
)

// Certificate seed suffixes. The two ends of a connection derive different identities from
// the same passphrase so that a peer cannot be reflected back onto itself; every derivation
// must go through one of these. They forward to package identity.
const (
	CertSuffixServer = identity.CertSuffixServer
	CertSuffixClient = identity.CertSuffixClient
)

// GenerateDeterministicCert forwards to identity.GenerateDeterministicCert, which other
// programs can import without this package.
func GenerateDeterministicCert(seed string) (tls.Certificate, error) {
	return identity.GenerateDeterministicCert(seed)
}
