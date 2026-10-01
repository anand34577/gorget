# Reproducible builds and verification

You should be able to confirm that a release was built from the published source.

## Reproducible

The Go binaries (`gorget-server`, `gorget`) are built with `-trimpath -buildvcs=false -ldflags "-s -w -buildid="` and a pinned Go toolchain version (`go.mod`), `CGO_ENABLED=0`. Building the same commit and version string twice, from different directories and with different build caches, yields byte-identical files:

```sh
make repro            # builds twice and compares: "reproducible: gorget-server <sha256>"
```

To verify a release yourself: check out the tag, run the same build command with `VERSION=<tag>`, and compare the SHA-256 with `SHA256SUMS`.

Not byte-reproducible today: the web console (npm bundles embed no timestamps but depend on the npm resolver; use `npm ci` with the lockfile), the tray app (cgo, system web view), and the Android APK (Gradle, signing). Their inputs are pinned by lockfiles.

## Signed and listed

Every release publishes:

- `SHA256SUMS` for every file;
- a keyless **cosign** signature of `SHA256SUMS` (`SHA256SUMS.sigstore.json`), made in the release workflow with the repository's GitHub identity:

  ```sh
  cosign verify-blob --bundle SHA256SUMS.sigstore.json \
    --certificate-identity-regexp 'https://github.com/anand34577/gorget/.*' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
  sha256sum -c SHA256SUMS
  ```

- a CycloneDX **SBOM** (`gorget-sbom.cdx.json`) listing every Go dependency.

The Android APK is signed with the project's own key; its certificate fingerprint is published with each release. Windows and macOS installers are not code-signed or notarised yet (no certificates): verify them with the checksums.
