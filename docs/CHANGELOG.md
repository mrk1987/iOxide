# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Separate desktop application with iOxide branding, guided account setup,
  conditional browser verification/2FA/mailbox-password screens, CalDAV client
  guides, Tailscale detection/help and HTTPS connection checks.
- Native tray menu, OS credential-store integration, optional start at login,
  masked connection credentials and measured calendar/client status.
- Desktop CI builds for Windows, macOS and Linux on amd64 and ARM64.
- Desktop test builds are published as pre-releases from `desktop-v*` tags,
  starting with `desktop-v1.0.0-beta.1`. They do not replace the CLI release.

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
