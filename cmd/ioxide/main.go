// Command ioxide is a CalDAV proxy for Proton Calendar.
//
// It logs in to a Proton account once, stores the session encrypted under a
// generated bridge password, and serves the account's calendars over CalDAV
// to any client that presents that password.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mrk1987/ioxide/protonmail"
)

const (
	defaultAPIEndpoint = "https://mail.proton.me/api"
	defaultAppVersion  = "Other"
	defaultListen      = "127.0.0.1:8081"
)

const usage = `usage: ioxide [options] <command> [arguments]

Commands:
  auth <username>            Log in to Proton and print a bridge password
  import-session <username>  Store a session copied from a logged-in Proton web tab
  status                     List stored accounts
  serve                      Run the CalDAV server

Options:
  -listen <address>      Address to listen on (default %q)
  -tls-cert <file>       TLS certificate; with -tls-key, serve HTTPS
  -tls-key <file>        TLS private key
  -api-endpoint <url>    Proton API endpoint (default %q)
  -app-version <string>  Client version reported to Proton (default %q)
  -log-requests          Log every CalDAV request (method, path, status)
  -debug                 Log every Proton API request and response
`

type options struct {
	debug       bool
	logRequests bool
	apiEndpoint string
	appVersion  string
	listen      string
	tlsCert     string
	tlsKey      string
}

func (o *options) newClient() *protonmail.Client {
	return &protonmail.Client{
		RootURL:    o.apiEndpoint,
		AppVersion: o.appVersion,
		Debug:      o.debug,
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ioxide:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var opts options
	fs := flag.NewFlagSet("ioxide", flag.ContinueOnError)
	fs.BoolVar(&opts.debug, "debug", false, "")
	fs.BoolVar(&opts.logRequests, "log-requests", false, "")
	fs.StringVar(&opts.apiEndpoint, "api-endpoint", defaultAPIEndpoint, "")
	fs.StringVar(&opts.appVersion, "app-version", defaultAppVersion, "")
	fs.StringVar(&opts.listen, "listen", defaultListen, "")
	fs.StringVar(&opts.tlsCert, "tls-cert", "", "")
	fs.StringVar(&opts.tlsKey, "tls-key", "", "")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, defaultListen, defaultAPIEndpoint, defaultAppVersion) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	cmd := fs.Arg(0)
	var cmdArgs []string
	if fs.NArg() > 1 {
		cmdArgs = fs.Args()[1:]
	}
	switch cmd {
	case "auth":
		username, err := singleArg(cmdArgs, "auth <username>")
		if err != nil {
			return err
		}
		return cmdAuth(&opts, username)
	case "import-session":
		username, err := singleArg(cmdArgs, "import-session <username>")
		if err != nil {
			return err
		}
		return cmdImportSession(&opts, username)
	case "status":
		return cmdStatus()
	case "serve":
		return cmdServe(&opts)
	case "", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func singleArg(args []string, usage string) (string, error) {
	if len(args) != 1 || args[0] == "" {
		return "", fmt.Errorf("usage: ioxide %s", usage)
	}
	return args[0], nil
}
