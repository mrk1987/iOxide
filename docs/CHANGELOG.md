# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.2.0] - 2026-11-21

The desktop app leaves beta. CLI and desktop are released together as 1.2.0.

### Changed

- Desktop: the first release that is not a pre-release. CLI and desktop are
  published together in one release (`v1.2.0`; its desktop files have their own
  `SHA256SUMS-desktop`). Later releases are built by one workflow with one
  `SHA256SUMS` and one provenance bundle; `desktop-v*` tags are
  no longer used. The macOS bundle and
  the Windows manifest carry version 1.2.0 (they said 1.0.0 and 0.1.0).
- `main` is protected: every change goes through a pull request that must pass
  CI, and every commit must be signed off (DCO), checked in CI.
- New documents: architecture (`docs/ARCHITECTURE.md`), security assessment
  (`docs/SECURITY-ASSESSMENT.md`) and governance (`.github/GOVERNANCE.md`);
  CONTRIBUTING.md now covers sign-off and how dependencies are chosen and
  tracked.
- Desktop: Wails 3.1.0 (from 3.0.0). Fixes include the tray tooltip, a tray
  created after start-up no longer being dropped, and WebView2 request bodies
  without Content-Length (#7).

### Fixed

- README security notes still said `auth.json` holds the Proton passwords; it
  has held only the session and derived key passphrases since 1.1.0.
- Desktop Settings said "Passwords are kept in your OS credential store",
  which read as if the Proton password were stored. It now says the Proton
  password is not stored and only bridge passwords are.

## [1.1.0] - 2026-11-20

Desktop pre-release: 1.0.0-beta.3. Both were rebuilt on 2026-11-20 to include
the fix for #8.

### Fixed

- Creating or changing events failed with Proton error `[2001] Provide data
  signed using the address key` on accounts with more than one address
  (aliases, `pm.me`, custom domains). Events were signed with the first
  address's key; they are now signed with the primary key of the address that
  owns the calendar (#8).

### Added

- Desktop: a "Start minimized to tray" setting. When iOxide starts at login it
  stays in the tray, unless the bridge cannot start or an account needs its
  bridge password.

### Security

- The Proton login password and mailbox password are no longer stored. Like
  Proton Bridge, iOxide keeps only the session tokens and the derived
  passphrases that unlock the account's keys. Records written by older
  versions are rewritten without passwords the first time they are opened.
- When Proton ends a session (password change, sign out everywhere), iOxide
  now asks you to sign in again instead of logging in with a stored password.
  The desktop app shows **Sign in again** and keeps the account's bridge
  password, so calendar apps need no changes.
- CLI and desktop releases carry signed build provenance attestations; check an
  archive with `gh attestation verify <archive> -R mrk1987/iOxide`.

### Changed

- Desktop: start at login and start minimized switch on once, after the first
  account is set up. A choice made in Settings is never overridden.
- Desktop module: `go.mod` now also targets Go 1.27.4 (`go mod tidy -diff` in
  desktop CI failed after the toolchain move).
- Build with Go 1.27.4. Go 1.26.0 carried standard-library vulnerabilities that
  `govulncheck` flagged in CI.
- Rewrote the README for 1.0 and corrected the sync description: changes are
  followed every minute and the calendar list is re-read every 15 minutes.

## [1.0.1] - 2026-11-13

### Added

- Separate desktop application with iOxide branding, guided account setup,
  conditional browser verification/2FA/mailbox-password screens, CalDAV client
  guides, Tailscale detection/help and HTTPS connection checks.
- Native tray menu, OS credential-store integration, optional start at login,
  masked connection credentials and measured calendar/client status.
- Desktop CI builds for Windows, macOS and Linux on amd64 and ARM64.
- Desktop test builds are published as pre-releases from `desktop-v*` tags,
  starting with `desktop-v1.0.0-beta.1`. They do not replace the CLI release.

### Fixed

- Desktop sign-in no longer waits forever after the human-verification
  challenge (`desktop-v1.0.0-beta.2`). Proton's challenge page cannot report
  back to a local helper page, so the app now opens `verify.proton.me` and
  retries sign-in when you confirm the challenge is done.
- `ioxide auth` completes human verification the same way: it opens
  `verify.proton.me`, waits for you to press Enter and retries the login. The
  old helper page only worked through a manual DevTools workaround.

### Changed

- Shared the authenticated CalDAV handler between CLI and desktop; tied background
  cache workers to the owning server's lifetime and locked credential updates
  across processes.
- macOS download filenames use `macos` instead of Go's internal `darwin` name.
- Updated checkout, Go setup, artifact upload/download and release publishing
  actions. Release workflow changes now build and verify all six archives
  before publication; branch and pull-request checks do not publish a release.
- Grouped application packages under `internal/`, deployment examples under
  `deploy/`, artwork and the changelog under `docs/`, and contribution policies
  under `.github/`.

### Removed

- Unused mail-era account fields, key metadata and an unused single-event
  write wrapper. Account and address key retrieval now share `account.go`.

## [1.0.0] - 2026-11-12

### Changed (breaking)

- iOxide is now a CalDAV proxy only. The IMAP, SMTP and CardDAV servers and
  the `import-messages`, `export-messages`, `export-secret-keys` and `sendmail`
  commands inherited from hydroxide have been removed.
- The `caldav` command is now `serve`.
- `-caldav-host` and `-caldav-port` are replaced by a single
  `-listen <address>` flag (default `127.0.0.1:8081`).
- `-tls-client-ca` has been removed. `-tls-cert` and `-tls-key` now work for
  the CalDAV server; before, they were accepted and ignored.
- Credentials are stored under `ioxide/` in the user configuration directory
  instead of `hydroxide/`. An existing `hydroxide/auth.json` is copied over on
  first run, so no new login is needed.
- The `HYDROXIDE_BRIDGE_PASS` environment variable is gone; it was only used
  by the removed commands.
- The binary is `ioxide` (built from `./cmd/ioxide`), no longer
  `ioxide-caldav`.

### Changed

- Switched from a vendored fork of go-webdav to the released go-webdav v0.7.0.
  The `PROPPATCH` handling Apple's clients need now lives in iOxide.
- Updated every dependency to its current version.
- A calendar-query for components other than `VEVENT` now returns an empty
  result instead of a 500 error.
- Requests for objects that do not exist return 404 instead of 500.
- Internal errors are no longer sent to the client; they are logged instead.
- The server shuts down cleanly on SIGINT and SIGTERM.

### Fixed

- Data races on the per-user handler map, the session map, the Proton
  client's access token and the calendar cache under concurrent requests.
- Several requests failing with 401 at the same time could each try to
  renew the session and invalidate each other's refresh token. Renewal now
  happens once.
- `PUT` now honours `If-Match` and `If-None-Match`, so a client with a stale
  copy gets 412 instead of silently overwriting another edit.
- Sessions stored by `import-session` without a refresh token were never
  usable; they now work until the access token expires.
- Running `ioxide auth` again while the server is running takes effect
  without a restart.
- Decrypting a stored entry shorter than a nonce could panic.
- Running the binary with no arguments crashed instead of printing usage.

### Added

- Calendars are kept current from Proton's per-calendar change log, polled
  once a minute, instead of being listed in full every 15 minutes. Edits made
  elsewhere now show up within a minute, and a poll costs one small request.
- The first full load fetches pages from Proton in parallel.
- The decrypted calendars are cached on disk, encrypted under a key derived
  from the bridge password, so a restart serves them at once instead of
  spending minutes re-downloading and decrypting.
- Unit tests, CI (build, test, lint, vulnerability check), Dependabot,
  release builds, a Dockerfile and a systemd unit.
- Release archives for Linux, macOS and Windows on amd64 and arm64, with
  SHA-256 checksums. Release builds run tests and vet before packaging.

## Before this changelog

iOxide started as a fork of [zCri/hydroxide](https://github.com/zCri/hydroxide)
(CalDAV support for [emersion/hydroxide](https://github.com/emersion/hydroxide))
with fixes for real-world use against iOS Calendar: a `PROPPATCH` no-op,
full pagination of event listings, recovery of malformed events, verification
of author-less events, atomic writes of `auth.json`, browser-based human
verification on login, session import and an in-memory calendar cache.
