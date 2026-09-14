# Releases and Homebrew

Pushing a stable `vMAJOR.MINOR.PATCH` tag runs GoReleaser, then updates
`maheshrijal/homebrew-tap/Formula/mysq.rb` with the published version and SHA-256
checksums for macOS/Linux on amd64/arm64. Prerelease tags do not update the tap.
The updater preserves the formula's install and test blocks, rejects downgrades,
and refuses changed checksums for an existing version.

## One-time setup

Like blinkcli, zocli, and bislericli, this workflow uses the repository secret
`HOMEBREW_TAP_GITHUB_TOKEN`. Use a fine-grained PAT restricted to
`maheshrijal/homebrew-tap` with **Contents: Read and write**. GitHub cannot copy
the value out of another repository's Actions secrets.

Set the secret using an interactive prompt, which avoids putting its value in shell history:

```fish
gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo maheshrijal/mysq
```

Automated tap commits are unsigned; no GPG key or passphrase is required.
Missing credentials stop the Homebrew job. This does not undo the
already-published GitHub release.

## Recovery

After fixing credentials or a concurrent tap update, use GitHub Actions to
**Re-run failed jobs** on the release run. The Homebrew job checks out the current
tap and is a no-op if that version and its checksums are already published.
Pushes are ordinary fast-forward pushes; concurrent changes are never overwritten.
