# Governance

iOxide is a small project with one maintainer. This page says who decides,
who can change what, and what each role is responsible for.

## Roles

| Role | Who | Responsibilities |
|---|---|---|
| **Maintainer** | Andreas ([@mrk1987](https://github.com/mrk1987)) | Sets direction and scope; reviews and merges pull requests; cuts releases; answers bug and security reports (see [SECURITY.md](SECURITY.md)); keeps dependencies and CI current; administers the repository and its settings. |
| **Contributor** | Anyone who opens an issue or a pull request | Follows [CONTRIBUTING.md](CONTRIBUTING.md) and the [code of conduct](CODE_OF_CONDUCT.md); signs off every commit (DCO); keeps changes focused and tested. |
| **Automation** | Dependabot, GitHub Actions | Proposes dependency updates as pull requests; runs tests, lint, CodeQL, govulncheck and Scorecard; builds and attests releases. Has no merge rights. |

Changes are written with AI assistance (Claude Code). The maintainer is
responsible for every change that is merged.

## Who has access to sensitive resources

| Resource | Access |
|---|---|
| GitHub repository (admin, merge, settings, rulesets) | Maintainer only |
| Releases and tags | Maintainer, and the release workflows acting on a pushed tag |
| Private security advisories | Maintainer only |
| bestpractices.dev project entry | Maintainer (anyone with repository write access can edit it) |

There are no other collaborators. The maintainer's GitHub account uses passkeys.

## How decisions are made

The maintainer decides, in public where possible: in issues and pull requests.
The project's scope is fixed in [CONTRIBUTING.md](CONTRIBUTING.md#scope): Proton
Calendar over CalDAV, nothing else.

## Changing this

Granting anyone write or admin access is a governance change. It is recorded by
updating this file in a pull request before the access is granted.
