# Custom CLIProxyAPI builds

`patched` is upstream `main` plus the patches listed in `.fork-kit.toml`. Every
push to `patched` builds and publishes `ghcr.io/bierdav/cliproxyapi:<version>-custom.<n>`
and a GitHub release (`custom-release.yml`). `<version>` is the newest upstream
release tag in the merged history.

## Keeping up with upstream

[fork-kit](https://github.com/BierDav/fork-kit), run by BierDav/fork-kit's central `sync-forks.yml`,
merges upstream `main` every two hours into the branch `fork-kit/sync` and keeps
one pull request into `patched` open for it.

- **Clean merge, green checks, no upstream `.github/` changes:** the PR merges
  itself, and the push to `patched` releases.
- **Conflicts:** the PR is labelled `fork-kit:conflict` and lists the
  conflicting files with the patches they belong to. Resolve them on
  `fork-kit/sync`: `npx github:BierDav/fork-kit#v0.4.0 resolve`, or GitHub's
  conflict editor. Then merge the PR yourself.
- **Upstream changed `.github/`:** nothing is pushed until a human has reviewed
  the change. Run `sync-forks.yml` in BierDav/fork-kit with this repository and
  `allow-workflow-changes` once you have.
- **Holding a PR:** add the label `fork-kit:hold` to any PR into `patched`.
  The required check `fork-kit/hold` then fails and auto-merge goes off, so the
  PR cannot merge until the label is removed; the next fork-kit run turns
  auto-merge back on for a sync PR. It holds that PR only, not releases.

Required checks on `patched`: `test` (the antigravity translator tests, from
`custom-release.yml`), `build` (upstream's `pr-test-build.yml`) and
`fork-kit/hold`.

## Repository settings this relies on

- **The GitHub App** (fork-kit-bierdav) is installed on all repositories. Its
  key exists only in BierDav/fork-kit's `fork-kit` environment, where the
  central sync runs; this repository holds no key. `.github/workflows/fork-kit.yml`
  is only the guard, which runs on `GITHUB_TOKEN`.
- **A ruleset on `patched`:** a pull request and the required checks; no force
  pushes or deletion. Repository admins may bypass it.
- **Upstream's contributor PR guards are disabled in this fork**
  (`agents-md-guard.yml`, `pr-path-guard.yml`, `auto-retarget-main-pr-to-dev.yml`).
  They would close or fail the sync PR whenever upstream touches `AGENTS.md` or
  `internal/translator/`.

## Deploying

After a release, `custom-release.yml` dispatches selfhosted-platform's
`pin-cliproxy-image.yaml` on Gitea with the image it published. That workflow
checks the digest and that the release contains the one deployed now, runs the
gateway checks with the new binary, and pushes the pin; Argo CD rolls the
gateway. The repository secrets `CLIPROXY_DEPLOY_URL` (the Gitea dispatch API
URL) and `CLIPROXY_DEPLOY_TOKEN` (a `cliproxy-image-bot` token that can only
dispatch that repository's Actions) enable it. Every release deploys, whatever
merged into `patched`; to stop deploys, create `manifests/cliproxy/image-hold`
in selfhosted-platform (its `docs/cliproxy.md`). `fork-kit:hold` only keeps one
PR from merging.
