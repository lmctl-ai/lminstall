# Releasing lminstall (GitHub release and npm)

Push to `main`. Nothing else is needed, and `lmauth` is not involved.

1. CI: the release workflow tests, builds and publishes the GitHub release.
2. The last job (`npm`) starts `.github/workflows/npm-publish.yml` for that tag.
3. `npm-publish.yml` downloads the release binaries, checks them against `SHA256SUMS`, builds `@lmctl-ai/lminstall` (launcher) and one `@lmctl-ai/lminstall-OS-CPU` package per platform, and publishes them with `npm publish` through npm trusted publishing (OIDC). No npm token exists anywhere.
4. The run ends by installing the published launcher and running it. A red run means the version is not usable; fix and re-run with `gh workflow run npm-publish.yml -f tag=vX.Y.Z`. A version already on npm is skipped, so re-runs are safe.

Adding a platform or a binary: change the `--target` list in `npm-publish.yml`. Adding a brand-new package name is the one case that needs a person: npm only lets you attach a trusted publisher to a package that already exists, so publish the first version once with `lmauth npm-release` (one numbered page of approval links), then everything above applies.
