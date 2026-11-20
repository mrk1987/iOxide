package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	bridgeui "github.com/mrk1987/ioxide/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/zalando/go-keyring"
)

//go:embed all:frontend
var assets embed.FS

type osVault struct{}

func (osVault) Get(user string) (string, error) { return keyring.Get("iOxide", user) }
func (osVault) Set(user, key string) error      { return keyring.Set("iOxide", user, key) }
func (osVault) Delete(user string) error {
	err := keyring.Delete("iOxide", user)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Desktop is the deliberately small IPC surface. It exposes no shell, filesystem,
// arbitrary browser navigation, or plaintext credential snapshots.
type Desktop struct {
	core           *bridgeui.Service
	app            *application.App
	window         *application.WebviewWindow
	copyGeneration atomic.Uint64
	closing        atomic.Bool
}

func (d *Desktop) State() bridgeui.State { return d.core.State() }
func (d *Desktop) BeginLogin(username, password string) (bridgeui.State, error) {
	return d.afterSetup(d.core.BeginLogin(username, password))
}
func (d *Desktop) CompleteTOTP(code string) (bridgeui.State, error) {
	return d.afterSetup(d.core.CompleteTOTP(code))
}
func (d *Desktop) CompleteMailboxPassword(password string) (bridgeui.State, error) {
	return d.afterSetup(d.core.CompleteMailboxPassword(password))
}
func (d *Desktop) CancelLogin() { d.core.CancelLogin() }
func (d *Desktop) Start() error { return d.core.Start() }
func (d *Desktop) Stop()        { d.core.Stop() }
func (d *Desktop) UnlockAccount(user, password string) error {
	if err := d.core.UnlockAccount(user, password); err != nil {
		return err
	}
	d.afterSetup(d.core.State(), nil)
	return nil
}

// afterSetup switches on start at login and start minimized once, when the
// first account becomes usable. Failing to do so never fails the sign-in.
func (d *Desktop) afterSetup(state bridgeui.State, err error) (bridgeui.State, error) {
	if err != nil {
		return state, err
	}
	enable, claimErr := claimStartupDefaults(state)
	if claimErr != nil {
		log.Printf("desktop: could not save startup defaults: %v", claimErr)
	}
	if enable {
		if err := d.setAutostart(true); err != nil {
			log.Printf("desktop: could not enable start at login: %v", err)
		}
	}
	return state, nil
}
func (d *Desktop) RemoveAccount(user string) error            { return d.core.RemoveAccount(user) }
func (d *Desktop) RevealPassword(user string) (string, error) { return d.core.RevealPassword(user) }
func (d *Desktop) OpenVerification() error {
	url, err := d.core.VerificationURL()
	if err != nil {
		return err
	}
	return d.app.Browser.OpenURL(url)
}

func (d *Desktop) ContinueVerification() (bridgeui.State, error) {
	return d.core.ContinueVerification()
}

func (d *Desktop) CopyPassword(user string) error {
	password, err := d.core.RevealPassword(user)
	if err != nil {
		return err
	}
	if !d.app.Clipboard.SetText(password) {
		return errors.New("could not access the clipboard")
	}
	// Do not erase clipboard content the user copied after this password.
	generation := d.copyGeneration.Add(1)
	time.AfterFunc(45*time.Second, func() {
		if d.closing.Load() || d.copyGeneration.Load() != generation {
			return
		}
		if text, ok := d.app.Clipboard.Text(); ok && text == password {
			d.app.Clipboard.SetText("")
		}
	})
	return nil
}

func (d *Desktop) CopyText(text string) error {
	if len(text) > 2048 {
		return errors.New("text is too long")
	}
	if !d.app.Clipboard.SetText(text) {
		return errors.New("could not access the clipboard")
	}
	return nil
}

// SetAutostart is the user's own choice, so the one-time defaults no longer apply.
func (d *Desktop) SetAutostart(enabled bool) error {
	if err := updatePreferences(func(prefs *preferences) { prefs.StartupDefaultsApplied = true }); err != nil {
		return err
	}
	return d.setAutostart(enabled)
}

func (d *Desktop) setAutostart(enabled bool) error {
	if enabled {
		// The flag lets a login launch start in the tray. The macOS 13+
		// login item (SMAppService) cannot pass arguments and ignores it.
		return d.app.Autostart.EnableWithOptions(application.AutostartOptions{Arguments: []string{autostartFlag}})
	}
	return d.app.Autostart.Disable()
}

func (d *Desktop) AutostartEnabled() (bool, error) {
	status, err := d.app.Autostart.Status()
	return status.Enabled, err
}

// migrateAutostartEntry rewrites a login entry made by an older build so it
// carries the --autostart flag. It never turns start at login on. macOS is
// skipped: its login item takes no arguments.
func migrateAutostartEntry(d *Desktop) {
	if runtime.GOOS == "darwin" {
		return
	}
	if enabled, err := d.AutostartEnabled(); err == nil && enabled {
		if err := d.setAutostart(true); err != nil {
			log.Printf("desktop: could not update the start-at-login entry: %v", err)
		}
	}
}

func (d *Desktop) StartMinimized() bool {
	prefs, err := loadPreferences()
	return err == nil && prefs.StartMinimized
}

func (d *Desktop) SetStartMinimized(enabled bool) error {
	return updatePreferences(func(prefs *preferences) {
		prefs.StartMinimized = enabled
		prefs.StartupDefaultsApplied = true
	})
}

func (d *Desktop) OpenHelp(topic string) error {
	urls := map[string]string{
		"help":        "https://github.com/mrk1987/iOxide#readme",
		"tailscale":   "https://tailscale.com/download",
		"https":       "https://tailscale.com/docs/how-to/set-up-https-certificates",
		"serve":       "https://tailscale.com/docs/reference/tailscale-cli/serve",
		"davx":        "https://www.davx5.com/",
		"apple":       "https://apps.apple.com/us/app/calendar/id1108185179",
		"thunderbird": "https://www.thunderbird.net/",
	}
	url, ok := urls[topic]
	if !ok {
		return errors.New("unknown help topic")
	}
	return d.app.Browser.OpenURL(url)
}

func main() {
	setProcessIdentity()
	preview := flag.Bool("preview", false, "open the interface with sample data; no login, network setup or credential access")
	launchedAtLogin := flag.Bool(strings.TrimPrefix(autostartFlag, "--"), false, "set by the start-at-login entry; starts in the tray when start minimized is on")
	flag.Parse()
	core := bridgeui.New(osVault{})
	startHidden := false
	if !*preview {
		prefs, _ := loadPreferences()
		startHidden = shouldStartHidden(*launchedAtLogin, prefs, len(core.State().Accounts))
	}
	service := &Desktop{core: core}
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	icon, _ := assets.ReadFile("frontend/assets/ioxide-icon.png")
	trayCtx, cancelTray := context.WithCancel(context.Background())
	app := application.New(application.Options{
		Name: "iOxide", Description: "Proton Calendar in your calendar app", Icon: icon,
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(frontend)},
		Windows:  application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:    application.LinuxOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "io.github.mrk1987.ioxide.desktop", OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if service.window != nil {
				service.window.Show()
				service.window.Focus()
			}
		}},
		OnShutdown: func() { service.closing.Store(true); cancelTray(); core.Close() },
	})
	service.app = app
	url := "/"
	if *preview {
		url = "/?preview=1"
	}
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "iOxide", Width: 1080, Height: 800, MinWidth: 980, MinHeight: 760, URL: url, BackgroundColour: application.NewRGB(27, 28, 36), DevToolsEnabled: false, DefaultContextMenuDisabled: true, Hidden: startHidden})
	service.window = window
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { event.Cancel(); window.Hide() })
	show := func(view string) { window.Show(); window.Focus(); app.Event.Emit("navigate", view) }
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("iOxide")
	tray.OnClick(func() { show("dashboard") })
	menu := app.Menu.New()
	statusItem := menu.Add("Starting iOxide…").SetEnabled(false)
	accountsItem := menu.Add("No accounts connected").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("Open iOxide").OnClick(func(*application.Context) { show("dashboard") })
	menu.Add("Connection details").OnClick(func(*application.Context) { show("connection") })
	toggle := menu.Add("Stop bridge")
	toggle.OnClick(func(*application.Context) {
		if *preview {
			show("dashboard")
			return
		}
		go func() {
			if core.State().Running {
				core.Stop()
			} else {
				_ = core.Start()
			}
		}()
	})
	menu.AddSeparator()
	menu.Add("Settings").OnClick(func(*application.Context) { show("settings") })
	menu.Add("Help").OnClick(func(*application.Context) { _ = service.OpenHelp("help") })
	menu.AddSeparator()
	menu.Add("Quit iOxide").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			if !*preview {
				_ = core.Start()
				migrateAutostartEntry(service)
			}
			// A tray-only start must not hide a problem only the user can fix.
			watchUntil := time.Now().Add(2 * time.Minute)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				if *preview {
					statusItem.SetLabel("Preview mode")
					accountsItem.SetLabel("Sample account")
					toggle.SetLabel("Open preview")
				} else {
					state := core.State()
					if startHidden && time.Now().Before(watchUntil) && needsAttention(state) {
						watchUntil = time.Time{}
						application.InvokeAsync(func() { show("dashboard") })
					}
					label := "Bridge stopped"
					if state.Running {
						label = "Bridge running"
					}
					if state.Error != "" {
						label = "Bridge needs attention"
					}
					statusItem.SetLabel(label)
					tray.SetTooltip("iOxide - " + label)
					users := make([]string, 0, len(state.Accounts))
					for _, account := range state.Accounts {
						users = append(users, account.Username)
					}
					if len(users) == 0 {
						accountsItem.SetLabel("No accounts connected")
					} else {
						accountsItem.SetLabel(strings.Join(users, ", "))
					}
					if state.Running {
						toggle.SetLabel("Stop bridge")
					} else {
						toggle.SetLabel("Start bridge")
					}
				}
				select {
				case <-trayCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
