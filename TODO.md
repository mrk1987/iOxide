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

- [ ] **Smoke-test the desktop app on Wails 3.1.0 before the next desktop beta**
  (merged in #7 on 2026-11-20 after CI passed on all six platforms; not yet run
  by hand). Check the tray menu and tooltip, start minimized at login, and a
  full sign-in. The running desktop app is single-instance, even in
  `-preview`, so quit it first.

- [ ] **Close CodeQL alert #8 (`js/unvalidated-dynamic-method-call`)** (added
  2026-11-20, not critical). `desktop/frontend/app.js:214` calls
  `(screens[view] || welcome)()`, and in preview mode `view` comes from the URL
  (`?preview=1&screen=...`). `screen=hasOwnProperty` or `constructor` reaches an
  `Object.prototype` method, so the preview page throws or renders
  `[object Object]`. Only built-in methods are reachable and only on the
  local preview page, so there is no code execution. Fix: look up with
  `Object.hasOwn(screens, view)` (or make `screens` a `Map`) and fall back to
  `welcome`.
