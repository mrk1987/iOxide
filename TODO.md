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
