# Contributing

Thanks for helping. iOxide is a small project; the rules are short.

## Scope

iOxide does one thing: expose Proton Calendar over CalDAV. Pull requests that
add mail, contacts or other Proton services will not be merged. Proton Bridge
and other tools cover those.

## Reporting bugs

Open an issue with what you did, what you expected and what happened, the
iOxide version and the client you used. Never include your bridge password,
Proton password, tokens or `auth.json`. Output of `-debug` contains access
tokens; redact them.

Security issues go through [SECURITY.md](SECURITY.md), not the issue tracker.

## Making changes

`main` is protected: every change, the maintainer's included, goes through a
pull request and needs the CI tests (Linux, macOS, Windows) and lint to pass
before it can be merged.

1. Fork and branch from `main`.
2. Keep the change focused. One fix or feature per pull request.
3. Add or update tests where the code can be tested without a Proton account.
4. Make sure these pass:

   ```sh
   gofmt -l .        # prints nothing
   go vet ./...
   go test ./...
   ```

   CI also runs `staticcheck` and `govulncheck`.
5. Add a line under "Unreleased" in `docs/CHANGELOG.md`.
6. Say in the pull request how you tested it, including which CalDAV client
   you tried if the change touches the server.
7. Sign off every commit (see below). CI rejects a pull request with an
   unsigned commit.

## Sign-off (DCO)

Every commit must carry a `Signed-off-by:` line. By adding it you certify the
[Developer Certificate of Origin](https://developercertificate.org/): that you
wrote the change or otherwise have the right to submit it under the project's
licence. Git adds the line for you:

```sh
git commit -s
```

To sign off commits you already made on your branch:
`git rebase --signoff main`, then force-push the branch. Dependabot's commits
are exempt; they contain no human-written code.

## Dependencies

- **Choosing:** prefer the Go standard library. A new dependency needs a reason
  in the pull request: what it does that the standard library cannot, and that
  it is maintained and has a compatible licence.
- **Obtaining:** dependencies are Go modules, fetched by the Go toolchain and
  verified against the checksums in `go.sum` (and `desktop/go.sum`). Nothing
  is vendored or downloaded by scripts.
- **Tracking:** `go.mod` lists every direct dependency. Dependabot proposes
  updates weekly for Go modules, GitHub Actions and the Docker base images,
  and `govulncheck` runs in CI on every change. Actions and base images are
  pinned to exact commits and digests.

## Code style

- Standard Go: `gofmt`, short names, errors wrapped with `%w` and a
  `package:` prefix, no panics in library code.
- Comments explain why, not what. Keep the ones that describe a Proton quirk;
  they are the documentation for this API.
- No new dependencies without a reason in the pull request.

## Licence

By contributing you agree that your contribution is licensed under the MIT
licence in [LICENSE](../LICENSE), and you confirm this with the sign-off on
each commit. Roles and decision making are described in
[GOVERNANCE.md](GOVERNANCE.md).
