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
5. Add a line under "Unreleased" in `CHANGELOG.md`.
6. Say in the pull request how you tested it, including which CalDAV client
   you tried if the change touches the server.

## Code style

- Standard Go: `gofmt`, short names, errors wrapped with `%w` and a
  `package:` prefix, no panics in library code.
- Comments explain why, not what. Keep the ones that describe a Proton quirk;
  they are the documentation for this API.
- No new dependencies without a reason in the pull request.

## Licence

By contributing you agree that your contribution is licensed under the MIT
licence in [LICENSE](LICENSE).
