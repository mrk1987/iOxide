# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/mrk1987/iOxide/security/advisories/new)
rather than a public issue. You should get a first response within a week.

Include what the problem is, how to reproduce it and what an attacker gains.
Do not include real credentials or tokens.

## Supported versions

Only the latest release is supported. There are no backports.

## What is in scope

- Anything that leaks the bridge password, the Proton session or decrypted
  calendar data to someone who should not have it.
- Authentication bypasses in the CalDAV server.
- Problems in how `auth.json` is written or protected.

Proton's own API and services are out of scope; report those to Proton.

## Design notes for reviewers

- The server authenticates with HTTP Basic. The password is a 256-bit random
  key generated at login. It is compared in constant time against a SHA-256
  digest held in memory and is never sent to Proton.
- `auth.json` is encrypted with NaCl secretbox (XSalsa20-Poly1305) under that
  key and written atomically with mode `0600`.
- The server is meant to run on localhost or a private network behind TLS. It
  has no rate limiting.
