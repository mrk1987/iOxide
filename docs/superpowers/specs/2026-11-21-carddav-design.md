# CardDAV for Proton Contacts: design

Date: 2026-11-21. Status: draft, not yet approved. Open question for Andreas: may the read-only spike (Risks, item 1) run against his account, with him creating, editing and deleting one throwaway contact in the Proton web app? Without it, sync is built on a guessed response format.

## Intent

iOxide serves Proton Calendar over CalDAV. This adds Proton Contacts over
CardDAV **in the same app**: the same `ioxide serve` process and the same
desktop app, the same listener, the same account and the same bridge
password. One sign-in gives a user both their calendars and their contacts in
the apps they already use.

Why: upstream hydroxide has a CardDAV server, but it is a separate,
unmaintained piece (unchanged since 2022, with open bugs: empty listings,
"openpgp: incorrect key"). iOxide's reason to exist beyond hydroxide is one
well-behaved app for calendars and contacts, on the CLI and the desktop.
Contacts are not covered by Proton Bridge (mail only), so this does not
replace Bridge; it fills a gap next to it.

## Goals

1. CardDAV read and write of the account's Proton contacts: list, read,
   create, edit, delete. Proton has one address book per account; iOxide
   serves exactly one.
2. Works with iOS Contacts, macOS Contacts, Thunderbird and DAVx⁵ (Android),
   set up with the same server address, username and bridge password as the
   calendar account.
3. Edits made elsewhere (Proton web, Proton apps) reach clients within about
   a minute, as they do for calendars.
4. A restart serves contacts at once from an encrypted disk cache.
5. CLI and desktop both serve contacts with no extra setup. The desktop shows
   the contact count and has CardDAV steps in its client guides.

## Non-goals (for this release)

- Contact groups as editable objects. Proton groups are kept intact on edits
  (see Data safety) but are not exposed as CardDAV group cards.
- Creating or deleting address books.
- Mail (IMAP/SMTP). Decided separately, after a trial of upstream hydroxide.
- Proton's per-contact encryption settings and email "sign/encrypt" flags
  (`X-PM-*` keys and scheme fields): preserved, not editable.

## Anti-goals (a failure even if it works)

- **Losing contact data.** Clients rewrite the whole vCard and drop fields
  they do not understand (iOS removes all `X-PM-*` fields). An edit from a
  client must never remove Proton-only fields, groups or encrypted fields the
  client could not see. Deleting a field the client did show (a phone number)
  must still work.
- Breaking existing calendar setups: no calendar URL, port, path or password
  changes.
- A second port, a second password, or a second account record.
- Storing anything new in plaintext.

## Constraints

- `main` is protected: every change goes through a pull request with CI and
  DCO sign-off.
- Go 1.27.4, `CGO_ENABLED=0` for the CLI, go-webdav v0.7.0, go-vcard,
  ProtonMail go-crypto. No new heavy dependencies; any new one is MIT/BSD-type.
- Proton's API is private and can change. Endpoints come from Proton's open
  source web client (`ProtonMail/WebClients`,
  `packages/shared/lib/api/contacts.ts` and `events.ts`), not from
  hydroxide's 2020-era unversioned paths:
  - `GET contacts/v4/contacts` (paged), `GET contacts/v4/contacts/export`
    (paged, includes cards), `GET contacts/v4/contacts/{id}`
  - `POST contacts/v4/contacts`, `PUT contacts/v4/contacts/{id}`,
    `PUT contacts/v4/contacts/delete`
  - change log: `GET contacts/v6/events/latest`, `GET contacts/v6/events/{id}`
- The OpenSSF Baseline level 2 documents must stay true: update
  `docs/ARCHITECTURE.md` and `docs/SECURITY-ASSESSMENT.md`.

## Approaches considered

- **A. Same listener, path split (chosen by Andreas).** Calendars stay at
  `/caldav/`; contacts are served at `/carddav/`, both on the existing port
  (default 8081). `/.well-known/caldav` and `/.well-known/carddav` redirect to
  each protocol's own principal, so the WebDAV library needs no changes. One
  address and one password everywhere; existing Tailscale/reverse-proxy
  forwarding covers contacts too.
- **B. Second port for contacts (hydroxide's 8080).** Rejected: every phone
  setup needs a second forwarded port and address, the desktop's HTTPS check
  and Tailscale help must cover two ports, and it feels like two tools.
- Dropped: one combined principal for both protocols (needs library changes,
  no user gain); a separate `ioxide contacts` command (against "same app").

## Decided design

### URL layout

| Path | Served by |
|---|---|
| `/.well-known/caldav`, `/caldav/...` | existing CalDAV handler (unchanged) |
| `/.well-known/carddav` | redirect to `/carddav/` |
| `/carddav/` | CardDAV principal |
| `/carddav/addressbooks/` | address book home set |
| `/carddav/addressbooks/default/` | the one address book |
| `/carddav/addressbooks/default/<name>.vcf` | one contact |

Anything else returns 404. The account router in `internal/bridge` dispatches
by path after the existing Basic-auth check, so both protocols share one
authenticated session per account. Resource names for new contacts follow the
same approach the CalDAV side uses for client-chosen names.

**The address users type (call: Claude).** Guides, the README and the desktop
show the bare server address (`https://<host>` or `http://127.0.0.1:8081`)
for both calendars and contacts; clients find each part through its
`/.well-known/` address. Today's `/caldav/` address keeps working, so
existing calendar accounts need no change. The desktop's HTTPS check tests
both well-known addresses. Router tests cover discovery from the bare address
for both protocols.

**Failure isolation (call: Claude).** Contacts are set up lazily and
independently of calendars. If the user key will not unlock or the contacts
API fails, calendars keep serving; contacts requests return 503 and the
desktop shows the contacts error next to the contacts count. Only a session
Proton ended ("Sign in again") is shared by both.

### Keys

Proton encrypts and signs contacts with the **user key**, not an address key.
Today `auth.Session.PrivateKeys` holds only address keys (the user keyring is
used to unlock them and then dropped), which is the likely cause of
upstream's "openpgp: incorrect key". The session gains the unlocked user
keyring (primary key first). Contacts are read with the user keys, and new or
changed cards are encrypted to and signed with the primary user key. The
stored credentials do not change: the same derived passphrases unlock the
user key already.

### Card format at Proton

A Proton contact is a set of cards:

- signed cleartext card: `VERSION`, `PRODID`, `FN`, `UID`, `EMAIL`
  (plus `X-PM-LABEL`, `X-PM-GROUP`/`CATEGORIES` where Proton keeps them
  cleartext);
- signed and encrypted card: everything else.

Reading merges the cards into one vCard 4.0 and verifies signatures; a
contact whose signature fails is still served (logged), as Proton's own
clients do. Writing splits the client's vCard back into these cards.

**vCard version to clients (call: Claude).** Clients receive vCard 3.0, the
version Apple clients use and every target client reads; iOxide converts
between 3.0 and 4.0 at the boundary (go-vcard only converts to 4.0, so the
4.0 → 3.0 direction is iOxide code: `TYPE` parameters, `PHOTO`/`LOGO`
encoding, `KIND`). All merging and comparison happen on the 4.0 form.

### Data safety on edits

On `PUT` of an existing contact, iOxide starts from the client's vCard and
then carries over from the cached Proton version every field the client
cannot represent: all `X-PM-*` fields, group membership, and the email
grouping that links per-address settings. Fields the client did show and
removed stay removed. A test suite of real-world client round trips (iOS,
macOS, Thunderbird, DAVx⁵ shaped vCards) guards this.

The carry-over rule, precisely (call: Claude):

- **Always carried over:** every `X-PM-*` property, and a fixed list of
  standard properties that none of the four target clients can show or edit.
  The list starts as `CLIENTPIDMAP`, `KIND`, `GENDER`, `LANG` and `RELATED`,
  and is confirmed by the client round-trip tests (a property a target client
  does show is removed from the list, so deleting it there works).
- **Everything else follows the client**, including deletions.
- **Groups:** Proton group membership is never changed through CardDAV (groups
  are a non-goal). `CATEGORIES` sent by a client is ignored for existing and
  new contacts; existing membership is always restored.
- **Per-email settings** (`itemN.X-PM-ENCRYPT`, `X-PM-SIGN`, `X-PM-SCHEME`,
  pinned keys) follow an email by **exact address match**, case-insensitive,
  never by position. An address the client changed gets Proton's defaults,
  not another address's settings; a pinned key or encrypt flag must never
  move to a different address. Client item groups (iOS `itemN.X-ABLabel`) are
  renumbered so they cannot collide with Proton's groups.

### Sync and cache

Same model as calendars:

- First load: page through `contacts/v4/contacts/export`, decrypt, cache.
- Every minute: poll `contacts/v6/events/{last}` and apply creates, updates
  and deletes; a "refresh everything" signal triggers a full reload.
- The decrypted cache is persisted encrypted under the existing per-account
  cache key (from the bridge password), in its own file next to the calendar
  cache.
- Strong ETags from the content; `If-Match` / `If-None-Match` honoured, so a
  stale client gets 412 instead of overwriting an edit.
- A session that Proton ended shows "Sign in again" exactly as for calendars;
  contacts and calendars share that state.

### CLI

`ioxide serve` serves contacts with no new flags. README: CardDAV section,
client setup for contacts, "What works / does not work" updated.

### Desktop

- Status per account: "N calendars · M contacts", with last sync.
- Client guides gain contacts steps (iOS: Settings → Contacts → Add Account →
  Other → CardDAV; macOS Contacts; Thunderbird; DAVx⁵), using the same
  address, username and bridge password shown today.
- Tray tooltip/status includes contacts.

### Documentation and security

- `docs/ARCHITECTURE.md`: contacts data flow and the new cache file.
- `docs/SECURITY-ASSESSMENT.md`: contacts as a protected asset (decrypted
  contacts in memory, disk cache, in transit), the data-safety risk on edits.
- CHANGELOG entry; this becomes **1.3.0**.

## Risks to retire first

1. **The v6 contacts change log's response shape is unknown.** Before writing
   sync code, a read-only spike against a real account (Andreas' own, run
   locally, nothing written) records the `latest` and `events/{id}` responses
   for a create, edit and delete made in Proton's web app. If v6 turns out
   unusable, fall back to `core/v5/events/{id}`, which carries contact
   changes.
2. **Real card shapes.** The same spike exports a few contacts (structure
   only, values redacted in anything saved) to confirm the card split and the
   `X-PM-*` fields.
3. **Client quirks.** iOS and macOS rewrite vCards on every edit; verified by
   hand with real clients before release.

## Testing

- Fake Proton server tests, like `internal/protonmail/calendar_test.go`, for
  listing, paging, the change log and writes.
- Round-trip tests for the card split/merge and the data-safety carry-over.
- Fuzz tests for path parsing and vCard decoding, like the CalDAV ones.
- Router tests: calendar paths unchanged, contacts paths reach CardDAV,
  unknown paths 404, both well-known redirects.
- Manual check before release: iOS Contacts and one desktop client against
  Andreas' account, create/edit/delete in both directions.

## Left to the builder

- Package layout (expected: `internal/carddav` mirroring `internal/caldav`,
  contacts API in `internal/protonmail/contacts.go`) and how much of the
  calendar cache code to share versus copy.
- Exact ETag construction, cache file format, and logging wording.
- Order of pull requests, as long as each one leaves `main` releasable.
