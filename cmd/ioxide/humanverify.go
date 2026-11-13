package main

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

// maxVerificationRounds limits how often the user is sent back to the
// challenge when Proton keeps asking.
const maxVerificationRounds = 3

// verificationURL is Proton's own verification page for a challenge. Proton's
// embeddable challenge reports its result only to a parent frame on
// verify.proton.me, so a local helper page cannot receive it. Instead the
// account owner completes the challenge there, and the login is retried with
// the same challenge token, as Proton Mail Bridge does.
func verificationURL(hv *protonmail.HumanVerificationDetails) (string, error) {
	if hv == nil || hv.HumanVerificationToken == "" || len(hv.HumanVerificationMethods) == 0 {
		return "", errors.New("unsupported human verification method requested by Proton")
	}
	query := url.Values{}
	query.Set("methods", strings.Join(hv.HumanVerificationMethods, ","))
	query.Set("token", hv.HumanVerificationToken)
	return "https://verify.proton.me/?" + query.Encode(), nil
}

// waitForVerification opens the verification page and blocks until the user
// confirms they have completed it. It is a variable so tests can replace it.
var waitForVerification = func(pageURL string) error {
	fmt.Println()
	fmt.Println("Proton requires you to complete a human-verification challenge.")
	fmt.Println("Opening it in your browser:", pageURL)
	fmt.Println("If it does not open automatically, paste that URL into your browser.")
	openBrowser(pageURL)
	_, err := askLine("Press Enter after you have completed the challenge: ")
	return err
}

func openBrowser(target string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	_ = exec.Command(cmd, append(args, target)...).Start()
}
