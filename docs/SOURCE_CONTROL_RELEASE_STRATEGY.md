# Source Control & Release Strategy

## Source control & development workflow

- Development work by core contributors should be carried out on the main branch for most contributions, with the exception being longer projects (weeks or months worth of work) or experimental new versions of the transformer. For the exceptions, feature/hotfix branches should be used.
- All development work by non-core contributors should be carried out on feature/hotfix branches on your fork, pull requests should be utilised for code reviews and merged (**rebase!**) back into the main branch of the primary repo.
- All commits should follow the [commit guidelines](./COMMIT_GUIDELINES.md).
- Work should be committed in small, specific commits where it makes sense to do so.

## Release strategy

Tags used for releases need to be in the following format:

```
vMAJOR.MINOR.PATCH

e.g. v0.1.0, v1.0.0
```

Version selection is automated using [svu](https://github.com/caarlos0/svu) (Semantic Version Utility), which analyzes conventional commit messages since the last tag to determine the next version:

- `fix:` commits → patch bump (e.g. 0.1.0 → 0.1.1)
- `feat:` commits → minor bump (e.g. 0.1.1 → 0.2.0)
- `BREAKING CHANGE:` in commit body or `!` after type → major bump (e.g. 0.2.0 → 1.0.0)
- No releasable commits → no release PR created

## Release workflow

The release workflow (`.github/workflows/release.yaml`) uses three tools that each do one thing:

- **[svu](https://github.com/caarlos0/svu)** — calculates the next semantic version from conventional commits
- **[git-cliff](https://github.com/orhun/git-cliff)** — generates the changelog from conventional commits
- **[goreleaser](https://goreleaser.com/)** — builds artifacts, creates the GitHub release, publishes

### How it works

On every push to `main`, the workflow:

1. Checks if the push is a merged release PR. If so, creates the tag and runs goreleaser.
2. Otherwise, runs svu to determine if a new release is warranted.
3. If releasable commits exist, generates an updated `CHANGELOG.md` with git-cliff and creates or updates a release PR on a `release/vX.Y.Z` branch.

The release PR accumulates changes as you push to main. When you're ready to release:

1. Review the changelog and version in the release PR.
2. Merge the PR.
3. The merge commit (prefixed `chore: release vX.Y.Z`) triggers the workflow again, which:
   - Detects the release merge from the commit message
   - Creates the `vX.Y.Z` tag
   - Runs goreleaser to build artifacts and create the GitHub release

### Step-by-step release process

1. Work on features and fixes, pushing commits to main following [commit guidelines](./COMMIT_GUIDELINES.md).
2. After each push, the workflow automatically creates or updates a release PR with the computed next version and changelog.
3. When ready to release, review the release PR:
   - Check the version bump is correct (patch/minor/major)
   - Review the changelog entries
4. Merge the release PR.
5. The workflow creates the tag, builds all artifacts, and publishes the GitHub release.
6. The Bluelink Registry webhook receives the `published` event with all artifacts available.

### Manual dispatch

The workflow supports manual dispatch via `workflow_dispatch` with a version input (e.g., `0.1.1`). This is useful for retrying a failed goreleaser run. The tag must already exist on the remote.

### Why a single workflow

GitHub Actions does not trigger workflows from events created by the default `GITHUB_TOKEN`. This means if the version job creates a tag in one workflow, a separate tag-triggered release workflow would not fire.

The solution is to combine everything into a single workflow. The goreleaser job runs conditionally when a release PR merge is detected or when manually dispatched.

### Release artifacts

The following artifacts are produced by goreleaser and included in each release:

| Artifact | Naming Convention | Purpose |
|----------|-------------------|---------|
| Platform binaries | `bluelink-transformer-celerity_{version}_{os}_{arch}.zip` | Plugin binaries for each supported platform |
| Registry info | `bluelink-transformer-celerity_{version}_registry_info.json` | Metadata consumed by the Bluelink Registry (protocols, dependencies, UI config) |
| Docs | `bluelink-transformer-celerity_{version}_docs.json` | Generated plugin documentation (display name, abstract resource docs) |
| Checksums | `bluelink-transformer-celerity_{version}_SHA256SUMS` | SHA-256 checksums for all release files |
| GPG signature | `bluelink-transformer-celerity_{version}_SHA256SUMS.sig` | GPG signature of the checksum file |

The `bluelink-registry-info.json` is checked into the repository. The `docs.json` file is generated during the release workflow by the `bluelink-plugin-docgen` tool.

### Bluelink Registry integration

The Bluelink Registry at [registry.bluelink.dev](https://registry.bluelink.dev) automatically discovers and publishes new versions of the transformer plugin.

**How it works:**

1. When a plugin is first registered with the registry, a GitHub webhook is dynamically created on the transformer repository. The webhook is configured to listen for `release` events and is secured with an HMAC-SHA1 secret unique to the organisation.
2. When a release is published, GitHub sends a `POST` request to the registry's webhook endpoint (`/webhook/gh/{organisation}`).
3. The registry validates the webhook signature, checks that the event action is `published`, and extracts the release information.
4. The registry identifies the plugin type and ID from the repository name (e.g., `bluelink-transformer-celerity` becomes transformer `newstack-cloud/celerity`).
5. Release artifacts are downloaded and processed — registry info for metadata, docs for documentation, and platform binaries are catalogued with their checksums.

**Requirements for the transformer repository:**

- The repository name must follow the pattern `bluelink-transformer-{name}` for the registry to recognise it as a transformer plugin.
- Release tags must use semantic versioning with a `v` prefix (e.g., `v0.1.0`, `v1.0.0`).
- The `bluelink-registry-info.json` file must be present in the release with the required fields (`supportedProtocols`, `dependencies`, and optionally `ui.referencedLinkPlugins`).
- The `docs.json` file must be present with at least a `displayName` field.
- The release must not be a draft when the registry processes it — all artifacts must be available at the time the `published` event is received.

## Pre-releases

Pre-releases are used for ongoing unstable builds that are still published to the Bluelink Registry. They follow the pattern `vX.Y.Z-next.N` (e.g., `v0.2.0-next.1`, `v0.2.0-next.2`).

### Enabling pre-release mode

Set `"prerelease": true` in `.release.json`:

```json
{
    "prerelease": true
}
```

Commit and push this change. The next push to main with releasable commits will produce a pre-release version (e.g., `v0.2.0-next.0`). Subsequent pushes increment the suffix (`next.1`, `next.2`, etc.).

### How pre-release versions work

svu has a built-in `prerelease` subcommand. When pre-release mode is enabled:

- `svu prerelease --pre-release=next` is used instead of `svu next`
- The version is based on the next version that would be released (e.g., if the next stable would be `v0.2.0`, the pre-release is `v0.2.0-next.0`)
- Each subsequent pre-release increments the suffix automatically

### Cutting a stable release

When the pre-release is ready for a stable release:

1. Set `"prerelease": false` in `.release.json`
2. Commit and push

The workflow will produce a stable version (e.g., `v0.2.0`). The changelog and release notes for the stable release will include **all changes since the last stable tag** (e.g., everything since `v0.1.0`), not just changes since the last pre-release. This is achieved by passing `--ignore-tags ".*-.*"` to git-cliff for stable releases, which makes it skip pre-release tags when determining the version boundary.

### Pre-release changelog behavior

- **Pre-release changelog entries** show changes since the previous tag (including other pre-releases)
- **Stable release changelog entries** show all changes since the last stable release, collapsing all intermediate pre-releases into a single entry

This ensures that upgrading from `v0.1.0` to `v0.2.0` shows the complete picture of what changed, regardless of how many `next` releases were published in between.

## Changelog

The `CHANGELOG.md` file is automatically generated and updated by [git-cliff](https://github.com/orhun/git-cliff) as part of the release PR. The changelog is configured in `cliff.toml` and groups commits by type:

- **Features** — `feat:` commits
- **Bug Fixes** — `fix:` commits
- **Performance Improvements** — `perf:` commits
- **Dependencies** — `deps:` commits
- **Refactoring** — `refactor:` commits
- **Testing** — `test:` commits

Commits with types `docs`, `style`, `chore`, `ci`, `build`, and `wip` are excluded from the changelog.

goreleaser also generates release notes for the GitHub release page using similar conventional commit grouping (configured in `.goreleaser.yml`). The changelog in the repository provides a cumulative history across all versions, while the GitHub release notes show changes for a single version.
