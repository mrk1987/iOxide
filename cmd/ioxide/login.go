package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/protonmail"
)

// cmdAuth logs in to Proton interactively and stores the session.
func cmdAuth(opts *options, username string) error {
	c := opts.newClient()

	loginPassword, err := askPassword("Password")
	if err != nil {
		return err
	}

	a, err := login(c, username, loginPassword)
	if err != nil {
		return err
	}

	if a.TwoFactor.Enabled != 0 {
		if a.TwoFactor.TOTP != 1 {
			return errors.New("only TOTP is supported as a second factor")
		}
		code, err := askLine("2FA TOTP code: ")
		if err != nil {
			return err
		}
		scope, err := c.AuthTOTP(code)
		if err != nil {
			return err
		}
		a.Scope = scope
	}

	mailboxPassword := loginPassword
	if a.PasswordMode == protonmail.PasswordTwo {
		if mailboxPassword, err = askPassword("Mailbox password"); err != nil {
			return err
		}
	}

	return storeSession(c, a, username, loginPassword, mailboxPassword)
}

// login performs the SRP login. If Proton gates it behind a human
// verification challenge, the user completes the challenge on Proton's
// verification page and the login is retried with the challenge token.
//
// Passing a real client version with -app-version avoids the challenge in
// most cases; this is the fallback for when that stops working.
func login(c *protonmail.Client, username, password string) (*protonmail.Auth, error) {
	attempt := func() (*protonmail.Auth, error) {
		info, err := c.AuthInfo(username)
		if err != nil {
			return nil, err
		}
		return c.Auth(username, password, info)
	}
	defer c.ClearHumanVerification()

	a, err := attempt()
	for round := 0; ; round++ {
		var apiErr *protonmail.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != protonmail.CodeHumanVerificationRequired || apiErr.HV == nil {
			return a, err
		}
		if round == maxVerificationRounds {
			return nil, errors.New("human verification was not accepted by Proton; try again later or use import-session")
		}
		if round > 0 {
			fmt.Println("Proton has not confirmed the verification yet.")
		}
		page, pageErr := verificationURL(apiErr.HV)
		if pageErr != nil {
			return nil, pageErr
		}
		if waitErr := waitForVerification(page); waitErr != nil {
			return nil, waitErr
		}
		c.SetHumanVerification(apiErr.HV.HumanVerificationToken, strings.Join(apiErr.HV.HumanVerificationMethods, ","))
		a, err = attempt()
	}
}

// cmdImportSession stores a session copied from a logged-in Proton web tab
// instead of logging in, which sidesteps the login challenge entirely.
func cmdImportSession(opts *options, username string) error {
	fmt.Println("Paste the session values from a logged-in Proton web session:")
	uid, err := askLine("  UID (x-pm-uid header): ")
	if err != nil {
		return err
	}
	accessToken, err := askLine("  Access token (Authorization header without 'Bearer '): ")
	if err != nil {
		return err
	}
	refreshToken, err := askLine("  Refresh token (optional, enables automatic renewal): ")
	if err != nil {
		return err
	}
	if uid == "" || accessToken == "" {
		return errors.New("UID and access token are both required")
	}
	mailboxPassword, err := askPassword("Mailbox password")
	if err != nil {
		return err
	}

	c := opts.newClient()
	c.SetSession(uid, accessToken)
	a := &protonmail.Auth{
		UID:          uid,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		PasswordMode: protonmail.PasswordSingle,
	}

	if err := storeSession(c, a, username, "", mailboxPassword); err != nil {
		return err
	}
	if refreshToken == "" {
		fmt.Println("No refresh token given: the session stops working when the access token expires.")
	}
	return nil
}

// storeSession unlocks the account keys to prove the session and passwords
// are right, then saves everything encrypted under a new bridge password.
func storeSession(c *protonmail.Client, a *protonmail.Auth, username, loginPassword, mailboxPassword string) error {
	keySalts, err := c.ListKeySalts()
	if err != nil {
		return fmt.Errorf("fetching key salts (is the session valid?): %w", err)
	}
	if _, err := c.Unlock(a, keySalts, mailboxPassword); err != nil {
		return fmt.Errorf("unlocking keys with the mailbox password: %w", err)
	}

	secretKey, bridgePassword, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	err = auth.EncryptAndSave(&auth.CachedAuth{
		Auth:            *a,
		LoginPassword:   loginPassword,
		MailboxPassword: mailboxPassword,
		KeySalts:        keySalts,
	}, username, secretKey)
	if err != nil {
		return err
	}

	fmt.Println("Bridge password:", bridgePassword)
	return nil
}

func cmdStatus() error {
	usernames, err := auth.ListUsernames()
	if err != nil {
		return err
	}
	if len(usernames) == 0 {
		fmt.Println("No stored accounts.")
		return nil
	}
	fmt.Printf("%d stored account(s):\n", len(usernames))
	for _, u := range usernames {
		fmt.Printf("  %s\n", u)
	}
	return nil
}

func askLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func askPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("standard input is not a terminal, cannot prompt for a password")
	}
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
