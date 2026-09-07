<!--
SPDX-FileCopyrightText: 2026 Tobias Mueller and Syncthing LUKS contributors
SPDX-License-Identifier: AGPL-3.0-or-later
-->
# Verifying releases

"Don't trust, verify." Every artifact this project ships (the Android APK,
the CLI binaries, the Debian packages, the SBOMs, and the signed F-Droid
repository index) is built in GitHub Actions and recorded in the public
Sigstore transparency log via `actions/attest-build-provenance` and
`actions/attest-sbom`. You do not have to trust the maintainer's laptop:
you can check that a given file was produced by this project's own CI, from
the source at a specific commit, and was not altered afterwards.

## 1. Verify build provenance

Requires `gh` >= 2.49 (`gh --version`).

```sh
gh attestation verify syncthing-socket.apk --repo muelli/syncthing-socket
gh attestation verify syncthing-socket-android-sbom.spdx.json \
  --repo muelli/syncthing-socket
```

A successful run confirms the file matches an attestation signed by this
repository's GitHub Actions workflow, at a specific workflow run and commit.
Run the same command against any release asset (CLI binaries, `.deb`
packages, `SHA256SUMS`) by pointing it at that file instead.

## 2. Verify the APK signing certificate

The APK is signed with a certificate derived from a single master seed (see
`signing/README.md`) and pinned in `signing/app-cert.pem`. That file appears
once the `Bootstrap signing identity` workflow has been run for the `app`
role; until then there is no release to verify. Confirm the APK
installed on your device, or downloaded from the self-hosted repository,
carries that exact certificate:

```sh
# From an APK file:
apksigner verify --print-certs syncthing-socket.apk \
  | sed -n 's/.*certificate SHA-256 digest: *//p'

# The pinned reference value:
openssl x509 -in signing/app-cert.pem -outform DER | sha256sum
```

The two SHA-256 digests must match. Android itself already enforces this at
install/update time by rejecting any APK whose signer differs from the one
already installed, so this check mainly matters the first time you install,
or when auditing the repository from outside a device.

## 3. Verify the F-Droid repository fingerprint

The self-hosted repository's fingerprint (shown on the landing page and in
the QR code) is the SHA-256 of the index-signing certificate:

```sh
openssl x509 -in signing/fdroid-cert.pem -outform DER | sha256sum
```

This must equal the fingerprint F-Droid shows when you add the repository,
and the one printed in the landing page footer. F-Droid clients pin the
fingerprint at add-time, so a compromised or replaced index-signing key
cannot silently start serving you different APKs under the same repository
entry; you would have to remove and re-add the repository, which is by
design (see the "Client-side pitfalls" note in the project's F-Droid
tooling documentation).

## 4. Reproduce the build yourself

Two independent CI jobs build the release APK from the same tag and
byte-compare the unsigned output before it is ever signed, so the signed
APK you install is provably built from the tagged source and nothing else.
To repeat that check yourself: check out the release tag, run the same
build steps as `.github/workflows/build.yml`'s `build-android` job
(`gradle assembleRelease` after `gomobile bind`), and compare your unsigned
APK to the released one with `apksigcopier compare`.

## What "attested" does not cover

An attestation proves *what* CI built and from *which* commit; it does not
review the source for you. Read the commit history, and treat any release
whose provenance check fails, or whose signer digest does not match, as
untrustworthy: stop, do not install it, and open an issue.
