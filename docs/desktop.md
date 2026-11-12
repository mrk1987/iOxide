# iOxide Desktop

iOxide has two separate executables:

| Executable | Purpose |
|------------|---------|
| `ioxide` / `ioxide.exe` | Command-line bridge for servers, containers and headless use. |
| `ioxide-desktop` / `ioxide-desktop.exe` | Desktop bridge with account setup, a native window and a tray menu. On macOS it is packaged as `iOxide.app`. |

The desktop app is in development. The `v1.0.0` release remains the CLI release;
desktop CI builds are separate artifacts, not a replacement for that release.

## Setup

1. Sign in to Proton. Verification opens in your browser when requested; an
   authenticator code or separate mailbox password is requested only when needed.
2. Choose whether your calendar app runs on this computer or another device.
3. For another device, configure Tailscale HTTPS forwarding or enter an existing
   HTTPS reverse-proxy address. The app detects Tailscale and provides instructions;
   it does not modify your Tailscale configuration.
4. Choose your calendar app, then use the supplied address, username and bridge
   password. The bridge password is different from your Proton password.

The HTTPS test checks the address from the bridge computer. It does not prove
that your phone is connected. Readiness shows real cache counts and authenticated
CalDAV activity, without estimated progress percentages.

Keep the bridge computer awake and connected. Closing the window leaves iOxide
running in the tray. The tray menu provides account/status information, connection
details, start/stop, settings, help and quit. Quit stops this desktop instance's
bridge. Start at login is opt-in.

## Existing CLI accounts and credential storage

The desktop uses the same encrypted `ioxide/auth.json` format as the CLI.
Enter an existing CLI account's bridge password once to unlock it in the desktop.
Bridge passwords are saved in Windows Credential Manager, macOS Keychain or the
Linux Secret Service. New sign-in does not fall back to storing that key as plain
text when the credential store is unavailable.

Running the CLI server and desktop server on port 8081 at the same time is not
supported. The desktop reports an occupied port and does not take over the other
process. Credential updates use an OS file lock so concurrent writers cannot
silently overwrite each other. New desktop sign-in refuses to replace an account
that another process added during setup.

Passwords and tokens never enter browser storage. Passwords are masked by default;
explicit reveal lasts 20 seconds. Copied bridge passwords clear after 45 seconds
while iOxide is running, provided the clipboard has not changed. Removing an
account deletes its local encrypted credentials and saved bridge key, not the
calendars in Proton. It does not revoke other Proton sessions.

## Build

The desktop is a separate Go module under `desktop/`; the root module and CLI do
not depend on Wails or the desktop credential-store library.

On Windows, with Go 1.26.11 or newer and WebView2 installed:

```powershell
.\desktop\build.ps1
# ARM64:
.\desktop\build.ps1 -Architecture arm64
```

The output is `dist/ioxide-desktop-windows-<arch>/ioxide-desktop.exe`. The script
uses Wails 3.0.0 to generate native icon resources from the icon-only iOxide art.
No frontend package installation or JavaScript bundler is required.

On macOS, build from `desktop/` with the Xcode command-line tools installed:

```sh
go build -tags production -o ../dist/ioxide-desktop .
```

Desktop CI builds both Mac architectures as ad-hoc-signed `.app` bundles.
These development bundles are not Developer ID signed or notarized.

On Linux, install GTK 4, WebKitGTK 6 and X11 development libraries first. For
Ubuntu 24.04:

```sh
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev libx11-dev
cd desktop
go build -tags production -o ../dist/ioxide-desktop .
```

A working desktop credential store and tray implementation are needed. Use the
CLI on headless systems.

## Preview and checks

Run the desktop executable with `-preview` to inspect screens using sample data.
Preview does not sign in, start the CalDAV server, read credentials or change
Tailscale settings. A separate screen selector appears only in preview mode.

The static interface can also be previewed in a browser:

```sh
python -m http.server 18764 --bind 127.0.0.1 --directory desktop/frontend
# Open http://127.0.0.1:18764/?preview=1
```

Without `?preview=1`, the interface requires the native desktop runtime. It is not
a web-based remote administration service.

Validation covers login-stage enforcement, cancellation during credential-store
access, concurrent account creation, occupied ports, removal failures, verification
origin checks and cache-worker shutdown. Desktop CI tests and builds native
Windows, macOS and Linux targets on amd64 and ARM64. Real account sign-in and
calendar-client acceptance still need an interactive test by the account owner.

## Design

The interface uses iOxide's existing artwork, charcoal panels and cyan/violet
accents. Setup is split into short screens that fit the default and minimum
window sizes. Dashboard and Settings have one bridge-status badge, in the same
upper-right position. The sidebar contains the logo, accounts and navigation.

Calendar-app icons are bundled locally; their sources and ownership notices are
in `desktop/frontend/assets/SOURCES.md`. Reference screenshots contain personal
account information and stay ignored locally.

Framework documentation: https://v3.wails.io/.
