package main

// OpenPGP key derivation for the `debian` role, used to sign the APT repository's
// Release file.
//
// Unlike the X.509 roles, this one really is reproducible from the seed alone, so nothing
// has to be pinned for correctness. Two properties make that work:
//
//   - An OpenPGP v4 fingerprint is SHA-1 over the public key packet: version, creation
//     time, algorithm and key material. Pin the creation time to a constant and the
//     fingerprint is a pure function of the derived key.
//   - Ed25519 signatures are deterministic by construction (RFC 8032), so the user-ID
//     self-signature is byte-identical every time too, given a pinned signature creation
//     time. This is exactly what ECDSA could not offer.
//
// The public key is still committed under signing/ and checked, because a mismatch means
// the wrong seed and should stop a release rather than quietly publish packages signed by
// an unknown key.
//
// Packet formats follow RFC 4880, with EdDSA as algorithm 22 (RFC 8032 keys, encoded per
// the "EdDSA legacy" convention that GnuPG has interoperated on for years).

import (
	"crypto/ed25519"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	pgpTagSignature = 2
	pgpTagSecretKey = 5
	pgpTagPublicKey = 6
	pgpTagUserID    = 13

	pgpAlgoEdDSA    = 22
	pgpHashSHA256   = 8
	pgpSigTypePosit = 0x13 // positive certification of a user ID

	// pgpKeyCreationTime is part of the identity: change it and the fingerprint changes.
	// 2024-01-01T00:00:00Z, matching the X.509 roles' NotBefore.
	pgpKeyCreationTime = 1704067200

	pgpUserID = "Syncthing LUKS APT repository <noreply@github.com>"
)

// ed25519OID is 1.3.6.1.4.1.11591.15.1.
var ed25519OID = []byte{0x2B, 0x06, 0x01, 0x04, 0x01, 0xDA, 0x47, 0x0F, 0x01}

// deriveEd25519Key derives the OpenPGP signing key for a role from the master seed.
func deriveEd25519Key(seed, role string) (ed25519.PrivateKey, error) {
	okm, err := expand(seed, role, ":openpgp-ed25519", ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(okm), nil
}

// mpi encodes a big-endian integer in OpenPGP's multiprecision format: a 16-bit bit count
// followed by the minimal big-endian bytes.
func mpi(b []byte) []byte {
	i := 0
	for i < len(b) && b[i] == 0 {
		i++
	}
	b = b[i:]
	if len(b) == 0 {
		return []byte{0, 0}
	}
	bits := (len(b)-1)*8 + (8 - leadingZeros8(b[0]))
	out := make([]byte, 2, 2+len(b))
	binary.BigEndian.PutUint16(out, uint16(bits))
	return append(out, b...)
}

func leadingZeros8(x byte) int {
	n := 0
	for i := 7; i >= 0; i-- {
		if x&(1<<uint(i)) != 0 {
			break
		}
		n++
	}
	return n
}

// publicKeyBody is the v4 public key packet body, the thing the fingerprint is taken over.
func publicKeyBody(pub ed25519.PublicKey) []byte {
	var b []byte
	b = append(b, 4)
	b = binary.BigEndian.AppendUint32(b, pgpKeyCreationTime)
	b = append(b, pgpAlgoEdDSA)
	b = append(b, byte(len(ed25519OID)))
	b = append(b, ed25519OID...)
	// Public point in prefixed native form: 0x40 followed by the 32-byte value.
	point := append([]byte{0x40}, pub...)
	b = append(b, mpi(point)...)
	return b
}

// secretKeyBody is the public key body plus the unencrypted secret scalar. The seed is the
// only place key material is stored, so protecting this at rest would be pointless: it
// exists for the few seconds gpg needs to import it.
func secretKeyBody(priv ed25519.PrivateKey) []byte {
	b := publicKeyBody(priv.Public().(ed25519.PublicKey))
	b = append(b, 0) // S2K usage 0: not encrypted
	secret := mpi(priv.Seed())
	b = append(b, secret...)
	var sum uint16
	for _, c := range secret {
		sum += uint16(c)
	}
	return binary.BigEndian.AppendUint16(b, sum)
}

func fingerprint(pub ed25519.PublicKey) []byte {
	body := publicKeyBody(pub)
	h := sha1.New()
	h.Write([]byte{0x99})
	_ = binary.Write(h, binary.BigEndian, uint16(len(body)))
	h.Write(body)
	return h.Sum(nil)
}

func keyID(pub ed25519.PublicKey) []byte { return fingerprint(pub)[12:] }

// packet wraps a body in a new-format OpenPGP packet header.
func packet(tag byte, body []byte) []byte {
	out := []byte{0xC0 | tag}
	n := len(body)
	switch {
	case n < 192:
		out = append(out, byte(n))
	case n < 8384:
		n -= 192
		out = append(out, byte(n/256+192), byte(n%256))
	default:
		out = append(out, 0xFF)
		out = binary.BigEndian.AppendUint32(out, uint32(n))
	}
	return append(out, body...)
}

func subpacket(typ byte, data []byte) []byte {
	body := append([]byte{typ}, data...)
	return append([]byte{byte(len(body))}, body...)
}

// selfSignature certifies the user ID with the key itself, which is what makes the key
// usable rather than a bare pile of key material.
func selfSignature(priv ed25519.PrivateKey, uid string) []byte {
	pub := priv.Public().(ed25519.PublicKey)

	var hashed []byte
	hashed = append(hashed, subpacket(2, binary.BigEndian.AppendUint32(nil, pgpKeyCreationTime))...)
	hashed = append(hashed, subpacket(27, []byte{0x03})...) // key flags: certify + sign
	hashed = append(hashed, subpacket(33, append([]byte{4}, fingerprint(pub)...))...)

	// Everything from the version byte through the hashed subpackets is covered by the
	// signature; the unhashed area is not.
	var signed []byte
	signed = append(signed, 4, pgpSigTypePosit, pgpAlgoEdDSA, pgpHashSHA256)
	signed = binary.BigEndian.AppendUint16(signed, uint16(len(hashed)))
	signed = append(signed, hashed...)

	keyBody := publicKeyBody(pub)
	h := sha256.New()
	h.Write([]byte{0x99})
	_ = binary.Write(h, binary.BigEndian, uint16(len(keyBody)))
	h.Write(keyBody)
	h.Write([]byte{0xB4})
	_ = binary.Write(h, binary.BigEndian, uint32(len(uid)))
	h.Write([]byte(uid))
	h.Write(signed)
	// Trailer: version, 0xFF, and the length of the signed data above.
	h.Write([]byte{0x04, 0xFF})
	_ = binary.Write(h, binary.BigEndian, uint32(len(signed)))
	digest := h.Sum(nil)

	// EdDSA signs the digest itself rather than re-hashing it.
	sig := ed25519.Sign(priv, digest)

	var body []byte
	body = append(body, signed...)
	unhashed := subpacket(16, keyID(pub)) // issuer key ID
	body = binary.BigEndian.AppendUint16(body, uint16(len(unhashed)))
	body = append(body, unhashed...)
	body = append(body, digest[0], digest[1])
	body = append(body, mpi(sig[:32])...)
	body = append(body, mpi(sig[32:])...)
	return packet(pgpTagSignature, body)
}

// transferableSecretKey and transferablePublicKey produce what gpg --import expects.
func transferableSecretKey(priv ed25519.PrivateKey) []byte {
	out := packet(pgpTagSecretKey, secretKeyBody(priv))
	out = append(out, packet(pgpTagUserID, []byte(pgpUserID))...)
	return append(out, selfSignature(priv, pgpUserID)...)
}

func transferablePublicKey(priv ed25519.PrivateKey) []byte {
	pub := priv.Public().(ed25519.PublicKey)
	out := packet(pgpTagPublicKey, publicKeyBody(pub))
	out = append(out, packet(pgpTagUserID, []byte(pgpUserID))...)
	return append(out, selfSignature(priv, pgpUserID)...)
}

// crc24 is the checksum OpenPGP armor carries.
func crc24(data []byte) uint32 {
	crc := uint32(0xB704CE)
	for _, b := range data {
		crc ^= uint32(b) << 16
		for i := 0; i < 8; i++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= 0x1864CFB
			}
		}
	}
	return crc & 0xFFFFFF
}

func armor(w io.Writer, blockType string, data []byte) error {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	encode := func(src []byte) string {
		var sb strings.Builder
		for i := 0; i < len(src); i += 3 {
			var buf [3]byte
			n := copy(buf[:], src[i:])
			v := uint32(buf[0])<<16 | uint32(buf[1])<<8 | uint32(buf[2])
			sb.WriteByte(b64[(v>>18)&0x3F])
			sb.WriteByte(b64[(v>>12)&0x3F])
			if n > 1 {
				sb.WriteByte(b64[(v>>6)&0x3F])
			} else {
				sb.WriteByte('=')
			}
			if n > 2 {
				sb.WriteByte(b64[v&0x3F])
			} else {
				sb.WriteByte('=')
			}
		}
		return sb.String()
	}

	body := encode(data)
	if _, err := fmt.Fprintf(w, "-----BEGIN %s-----\n\n", blockType); err != nil {
		return err
	}
	for i := 0; i < len(body); i += 64 {
		end := i + 64
		if end > len(body) {
			end = len(body)
		}
		if _, err := fmt.Fprintln(w, body[i:end]); err != nil {
			return err
		}
	}
	var sum [3]byte
	c := crc24(data)
	sum[0], sum[1], sum[2] = byte(c>>16), byte(c>>8), byte(c)
	if _, err := fmt.Fprintf(w, "=%s\n-----END %s-----\n", encode(sum[:]), blockType); err != nil {
		return err
	}
	return nil
}
