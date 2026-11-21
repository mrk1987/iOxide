# TODO

- [ ] **HTTPS by default for calendar-client connections, including localhost**
  (deferred 2026-11-13). Match Proton Mail Bridge's local connection-encryption
  baseline using HTTPS for CalDAV. Reuse the CLI's existing TLS approach in the
  desktop service; provide automatic local certificate generation, protected
  private-key storage, certificate renewal and clear client trust instructions.
  Do not silently fall back to plaintext HTTP. Preserve secure access from other
  devices through Tailscale Serve, a reverse proxy or a configured certificate;
  account for their existing localhost HTTP forwarding when designing migration.
  Update connection URLs, setup guidance and security indicators, and verify
  certificate trust with supported calendar clients. This concerns connection
  encryption, not a claim of overall security equivalence with Proton Bridge.
  Implementation is explicitly postponed.

- [ ] **Close CodeQL alert #8 (`js/unvalidated-dynamic-method-call`)** (added
  2026-11-20, not critical). `desktop/frontend/app.js:214` calls
  `(screens[view] || welcome)()`, and in preview mode `view` comes from the URL
  (`?preview=1&screen=...`). `screen=hasOwnProperty` or `constructor` reaches an
  `Object.prototype` method, so the preview page throws or renders
  `[object Object]`. Only built-in methods are reachable and only on the
  local preview page, so there is no code execution. Fix: look up with
  `Object.hasOwn(screens, view)` (or make `screens` a `Map`) and fall back to
  `welcome`.

- [ ] **Code signing for Windows through SignPath's free open-source program**
  (added 2026-11-21, not urgent). Free and works from GitHub Actions, but each
  release needs a manual approval in SignPath, and Windows shows
  "SignPath Foundation" as the publisher. Terms: https://signpath.org/terms.
  Before applying, the repo needs:
  1. A "Code signing policy" section in the README, linked from the releases,
     with SignPath's required sentence ("Free code signing provided by
     SignPath.io, certificate by SignPath Foundation", both linked), the roles
     (author, reviewer and approver: mrk1987) and a privacy statement.
  2. The privacy statement: iOxide sends nothing to networked systems other
     than Proton and the calendar clients the user configures.
  3. Uninstall instructions: quit, delete the files and the start-at-login
     entry, remove bridge passwords from the credential store and the
     `ioxide` config folder.
  4. Windows version info in `ioxide.exe` and `ioxide-desktop.exe`: product
     name "iOxide" and the release version, the same in every build.
  Then apply at https://signpath.org/apply (the form needs a browser) and add
  signing to `release.yml` before archiving, so checksums and provenance cover
  the signed files. macOS signing has no free option (Apple Developer Program,
  US$99 a year); Linux is covered by the build provenance.
