# Architecture

iOxide is a bridge: it signs in to Proton, decrypts calendars locally and
serves them over CalDAV so that ordinary calendar apps can read and write
Proton Calendar. This document describes the components, the actors that
interact with them and the data that flows between them. The security
assessment in [SECURITY-ASSESSMENT.md](SECURITY-ASSESSMENT.md) builds on it.

## Actors

| Actor | What it does |
|---|---|
| **Account owner** | Signs in once with their Proton credentials (and 2FA if enabled), copies the bridge password into their calendar apps, and runs the bridge. |
| **Calendar client** | iOS/macOS Calendar, Thunderbird, DAVx⁵, Home Assistant and others. Talks CalDAV to iOxide using the bridge password. |
| **Proton API** | `mail.proton.me/api`. Authenticates the account (SRP), issues session tokens, and stores encrypted calendar data. |
| **Operating system** | Provides the user's config and cache directories, and on the desktop the credential store (Windows Credential Manager, macOS Keychain, Secret Service on Linux). |
| **Reverse proxy or Tailscale** (optional) | Terminates TLS when calendar clients connect from other devices. |

## Components

```text
cmd/ioxide/         CLI: auth (sign in), import-session, status, serve (run the bridge)
desktop/            Desktop app (Wails): window, tray, setup flow, OS credential store
internal/auth/      Sign-in, session renewal and the encrypted auth store (auth.json)
internal/protonmail Proton API client: SRP login, keys, calendars, events, event crypto
internal/caldav/    CalDAV backend: calendar and event mapping, in-memory and disk cache
internal/bridge/    HTTP handler: Basic authentication and routing to each account's backend
internal/desktop/   Desktop service: accounts, bridge lifecycle, status for the UI
```

The CLI and the desktop app share everything under `internal/`. They differ only
in how they start the bridge and where the bridge password is kept.

## Data flows

### 1. Sign-in (once per account)

1. The owner enters the Proton username, password, and 2FA code if needed.
2. `internal/protonmail` runs Proton's SRP exchange. The password itself is never
   sent to Proton, and Proton's server proof is checked in constant time.
3. The mailbox password (the login password on single-password accounts)
   unlocks the account's private keys. iOxide keeps the derived per-key
   passphrases, not the password.
4. iOxide generates a random 256-bit **bridge password**, encrypts the session
   record with it (NaCl secretbox) and writes `auth.json` with mode `0600`.
5. The CLI prints the bridge password once. The desktop app stores it in the OS
   credential store.

What `auth.json` holds: the Proton session (UID, access token, refresh token)
and the derived key passphrases, encrypted under the bridge password. It holds
no Proton password.

### 2. Serving a calendar request

1. A calendar client sends a CalDAV request (PROPFIND, REPORT, GET, PUT, DELETE)
   with HTTP Basic credentials: the Proton username and the bridge password.
2. `internal/bridge` hashes the supplied password with SHA-256 and compares it
   in constant time with the digest of the running account's key. On a
   mismatch it answers `401`.
3. The request goes to that account's `internal/caldav` backend. Paths are
   checked against the exact calendar and event pattern; anything else is `404`.
4. Reads are answered from the decrypted in-memory cache.
5. Writes are parsed (iCalendar), split into Proton's signed and encrypted
   parts, signed with the primary key of the calendar's address, encrypted with
   the calendar key and sent to Proton in one batch request.

### 3. Keeping in sync

- Once a minute the backend reads Proton's calendar change log and applies
  changes to the cache. The calendar list is re-read every 15 minutes.
- The cache is saved to the user's cache directory, encrypted with a key derived
  from the bridge password (HKDF-SHA256, NaCl secretbox), mode `0600`. A damaged
  or tampered file is discarded and rebuilt from Proton.
- Access tokens are refreshed with the refresh token. Proton rotates the refresh
  token on every use, so the new one is written to `auth.json` at once. If Proton
  ends the session, the account shows "Sign in again" and nothing is retried with
  stored passwords (there are none).

## Trust boundaries

| Boundary | Crossing | Protection |
|---|---|---|
| Calendar client → iOxide | CalDAV over HTTP or HTTPS | Bridge password (Basic auth); TLS when not on localhost (built-in or a reverse proxy) |
| iOxide → Proton | HTTPS | TLS 1.2+ with certificate verification; SRP login; session tokens |
| iOxide → disk | `auth.json`, calendar cache | Encrypted under the bridge password; mode `0600`; atomic writes |
| Desktop app → OS credential store | bridge password | Protected by the user's OS account |

## Deployment shapes

- **Desktop:** the bridge listens on `127.0.0.1:8081`. Other devices connect through
  Tailscale Serve or a reverse proxy that terminates TLS.
- **CLI or container:** `ioxide serve` listens on `127.0.0.1:8081` by default, or on
  the address given with `-listen`, optionally with its own TLS certificate.
