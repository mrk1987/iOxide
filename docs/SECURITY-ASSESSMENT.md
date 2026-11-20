# Security assessment

Assessed for release 1.2.0 (CLI and Desktop), 2026-11-21. It is
reviewed again for every minor release and whenever authentication, storage or
the network-facing server changes. The components and trust boundaries are
described in [ARCHITECTURE.md](ARCHITECTURE.md).

## What needs protecting

1. **The Proton session.** With the access and refresh tokens, someone can act
   as the user in every Proton service the session covers, not only Calendar.
2. **The account's private keys** and the passphrases that unlock them. With
   them, someone can read and forge the user's encrypted data.
3. **Decrypted calendar data**, in memory, in the disk cache and in transit to
   calendar clients.
4. **The bridge password**, which unlocks all of the above locally.

## Most likely and most damaging problems

Ranked by likelihood times impact. Each has the mitigations in place today and
what remains.

### 1. The bridge is reachable from a network it should not be

*Impact: high. Likelihood: medium.* Users expose the CalDAV port to reach it from
a phone. Without TLS, the bridge password and calendar data cross the network in
clear text; the server has no rate limiting, so a reachable port can be
brute-forced in principle.

- In place: listens on `127.0.0.1` by default; the bridge password is a random
  256-bit key, so guessing is infeasible; built-in TLS (1.2+) and documented
  setups with Tailscale or a TLS reverse proxy.
- Remaining: no lockout or rate limiting; HTTPS by default is planned
  (TODO.md). Users who bind to `0.0.0.0` without TLS are at risk.

### 2. Local malware or another user reads the stored credentials

*Impact: high. Likelihood: medium.* Anything that runs as the user can read the
OS credential store and `auth.json`, and so recover the session.

- In place: no Proton password is stored (since 1.1.0), so a theft gives a
  session the user can end by signing out everywhere, not their password;
  `auth.json` and the cache are encrypted and mode `0600`; refresh tokens are
  rotated by Proton.
- Remaining: same-user malware is out of scope for a user-level bridge, as it is
  for Proton Bridge.

### 3. Malformed input from a calendar client or from Proton crashes or confuses the bridge

*Impact: medium (denial of service, wrong data written). Likelihood: medium.*
Calendar clients send diverse and sometimes broken iCalendar; events created by
other apps can be malformed too.

- In place: strict path validation; iCalendar parsed by a strict parser with a
  narrow repair step; Go is memory-safe; fuzz tests on path parsing, iCalendar
  decoding and event splitting.
- Remaining: fuzzing runs locally, not continuously in CI.

### 4. Writes signed or encrypted with the wrong key

*Impact: medium (failed writes or unreadable events). Likelihood: low.* Proton
validates signatures, so a wrong key is rejected rather than accepted.

- In place: events are signed with the primary key of the calendar's own
  address (fixed in issue #8); tests cover multi-address accounts.

### 5. Secrets leak through logs

*Impact: high. Likelihood: low.* `-debug` prints HTTP traffic, including access
tokens.

- In place: debug logging is off by default and documented as printing tokens;
  normal logs carry no credentials; the bug report guide tells users to redact.

### 6. A compromised dependency or build

*Impact: high. Likelihood: low.*

- In place: Dependabot and govulncheck; GitHub Actions pinned to commit SHAs;
  least-privilege workflow tokens; reproducible CLI builds; signed build
  provenance on every release; `main` only changes through pull requests that
  pass CI, with every commit signed off (DCO).
- Remaining: no SBOM is published yet; one maintainer reviews all changes;
  release binaries are not code-signed, so the build attestation is the way to
  check a download.

### 7. Proton changes its API

*Impact: medium (sync stops). Likelihood: medium.* iOxide uses Proton's private
web API, which can change without notice.

- In place: API errors surface in the status view, the log and, for writes,
  as a failed request in the calendar client.

## Out of scope

Proton's own servers and clients, the security of the calendar apps, and an
attacker with administrator or same-user access to the machine running iOxide.
