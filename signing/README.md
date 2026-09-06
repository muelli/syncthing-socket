# Pinned signing identities

Every signing identity this project uses is derived from one secret, the `MASTER_SECRET`
repository secret, plus a role label. The seed is the only secret state. This directory
holds the **public** halves, committed so that the derivation can be checked rather than
trusted.

| File | Role | Used for |
| --- | --- | --- |
| `app-cert.pem` | `app` | signing the Android APK |
| `fdroid-cert.pem` | `fdroid` | signing the F-Droid repository index |

## Why these are committed

A certificate is the identity, not merely a wrapper around one. Android compares the exact
certificate bytes to decide whether an update comes from the same signer, and an F-Droid
repository fingerprint is the SHA-256 of the index-signing certificate, which clients pin
when the repository is added.

ECDSA self-signatures are randomised, and Go goes out of its way to keep them that way:
`crypto/ecdsa` calls `randutil.MaybeReadByte`, which consumes zero or one bytes from the
caller's reader depending on Go's own global random source, precisely so that nobody can
rely on deterministic signing. So the certificate cannot be re-derived from the seed even
with a fixed random stream. It is minted once and pinned here; `scripts/generate_keystore.go`
then refuses to sign if the seed does not reproduce the pinned public key.

Before this existed, every CI run minted a fresh certificate from the same key, so each
release was a different signer and each repository publish a different fingerprint.

## Bootstrapping a role

Once per role, ever. Run the `Bootstrap signing identity` workflow
(`.github/workflows/bootstrap-signing.yml`) with the role name; it derives the key inside
CI, so the seed never leaves the secret store, and opens a pull request adding the
certificate here.

Re-running it for a role that already has a committed certificate produces a *different*
certificate and is an identity reset: every user must uninstall the app and remove and
re-add the repository. The workflow refuses unless `confirm_rotation` is set.

## Rotating

Treat as a last resort. A rotation requires updating everything that pins a digest:
this directory, the landing page footer, `docs/TRANSPARENCY.md`, the transparency
verifier, and `AllowedAPKSigningKeys` in fdroiddata if the app is listed there. Announce
it in the changelog as a one-time manual step for users.
