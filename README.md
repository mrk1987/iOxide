![iOxide](assets/iOxide-banner.png)

# <img src="assets/iOxide-logo.png" width="32" height="32" align="left" alt=""> iOxide

A CalDAV bridge exposing Proton Calendar, built specifically to sync a real
Proton account into iOS's native Calendar app (hence the name).

## Lineage

`emersion/hydroxide` → CalDAV support added by `zCri/hydroxide` (never merged
upstream, [PR #282](https://github.com/emersion/hydroxide/pull/282)) → this
fork. `emersion/hydroxide` was archived (read-only) 2026-08-02, so this is now
the actively-patched line rather than a fork awaiting upstream merge.

Six local patches were applied on top of `zCri/hydroxide` to make CalDAV
actually work end-to-end against a real account and survive on iOS: a
PROPPATCH no-op (Apple Calendar's account-setup probe), a `RecurrenceID`
type fix, verifying author-less calendar events (holidays/imports) against
the account's own keyring, full pagination with per-event skip-on-decode-
failure, and atomic `auth.json` writes.

It also incorporates several fixes ported from
[`mathewcsims/protoxide`](https://github.com/mathewcsims/protoxide) — itself
an abandoned one-day fork of `acheong08/ferroxide`/hydroxide with no ongoing
relationship to this repo, but a few of its fixes were real and worth
crediting directly: malformed-event recovery (`sanitizeICS`), verifying
author-less events against the account's own keyring, browser-based
human-verification (CAPTCHA) handling on login, and an `import-session`
command to seed auth from an existing logged-in Proton web session.

## What works

Full two-way sync confirmed against a real Proton account: iOS Calendar can
view, create, edit, and delete events against Proton Calendar, Proton
Holidays calendars, and shared/imported calendars with no author key.

## Known limitations

- CalDAV only — no IMAP/SMTP (inherited from hydroxide, unmaintained here;
  use a separate email bridge)
- Plaintext HTTP only; put a TLS terminator (e.g. `tailscale serve`) in front
  for real clients — iOS Calendar requires HTTPS
- Third-party, unofficial: a Proton API change can break this at any time,
  and there's no upstream to pull fixes from anymore

## Setup

```shell
git clone https://github.com/mrk1987/iOxide.git
cd iOxide
go build -o ioxide-caldav ./cmd/ioxide
```

Authenticate (mandatory: pass a real client `-app-version`, or Proton
CAPTCHA-gates the login — see operational notes for how to find a current
valid value):

```shell
./ioxide-caldav -app-version linux-bridge@3.25.0 auth <username>
```

This prints a one-time **bridge password** — not your Proton password — used
by CalDAV clients below. Credentials are stored encrypted under it at
`~/.config/hydroxide/auth.json` (path inherited from upstream, unchanged).

Run the CalDAV server:

```shell
./ioxide-caldav -app-version linux-bridge@3.25.0 -caldav-host 0.0.0.0 caldav
```

### iOS Calendar

Settings → Calendar → Accounts → Add Account → Other → Add CalDAV Account.
Server must be reachable over HTTPS (iOS refuses to save a plain-HTTP CalDAV
account) — front the plaintext listener with a TLS-terminating proxy.

## License

MIT, inherited from `emersion/hydroxide`.
