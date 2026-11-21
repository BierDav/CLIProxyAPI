# Custom CLIProxyAPI builds

The `patched` branch records the merged upstream commit in
`.custom-upstream-revision` and the release version in `.custom-upstream-tag`.
The sync workflow merges source updates and requests a custom release. It
does not copy upstream tags to the fork; the release workflow publishes its
own `<version>-custom.<n>` tag and multi-architecture image.

If the sync job reports a conflict or a change under `.github/workflows/`,
review that merge as a maintainer. The sync token cannot write workflows, so
the job aborts before pushing any refs. From a clean, full clone of the fork:

```sh
git switch patched
git pull --ff-only origin patched
git fetch --no-tags https://github.com/router-for-me/CLIProxyAPI.git main
upstream_revision=$(git rev-parse FETCH_HEAD)
upstream_tag=$(gh api repos/router-for-me/CLIProxyAPI/releases/latest --jq '.tag_name')
git merge --no-ff --no-commit "$upstream_revision"
```

Review `git diff --cached -- .github/workflows/`, resolve any conflicts and
preserve the fork's release and sync workflows. Update the markers for the
reviewed merge:

```sh
printf '%s\n' "$upstream_revision" > .custom-upstream-revision
printf '%s\n' "$upstream_tag" > .custom-upstream-tag
git add .custom-upstream-revision .custom-upstream-tag
```

Test the merge with `go test -v ./internal/translator/antigravity/...` and
`go build -o /tmp/cliproxy-fork-build ./cmd/server`. Commit and push `patched`
with a maintainer credential that can write workflows. The push starts the
custom release; verify both build architectures and the published release.
Dispatch `sync-upstream.yml` on `patched` if the push did not run it, so it
can verify that the fork is current. The cluster pins its image separately.
