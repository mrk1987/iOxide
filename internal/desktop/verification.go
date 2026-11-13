package desktop

import (
	"errors"
	"net/url"
	"strings"
)

// VerificationURL returns Proton's own verification page for the pending
// challenge. The account owner completes it in their browser; iOxide never
// sees or handles the challenge itself.
//
// Proton's embeddable challenge page reports its result only to a parent
// frame and refuses to be framed by other sites, so a local helper page can
// never receive it. This follows Proton Mail Bridge instead: open
// verify.proton.me, then retry sign-in with the same challenge token.
func (s *Service) VerificationURL() (string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, err := s.pendingAt("verification")
	if err != nil {
		return "", err
	}
	if p.challenge == nil || p.challenge.HumanVerificationToken == "" || len(p.challenge.HumanVerificationMethods) == 0 {
		return "", errors.New("this verification method is not supported; cancel and try signing in again")
	}
	query := url.Values{}
	query.Set("methods", strings.Join(p.challenge.HumanVerificationMethods, ","))
	query.Set("token", p.challenge.HumanVerificationToken)
	return "https://verify.proton.me/?" + query.Encode(), nil
}

// ContinueVerification retries sign-in after the account owner has completed
// the challenge in their browser.
func (s *Service) ContinueVerification() (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, err := s.pendingAt("verification")
	if err != nil {
		return s.State(), err
	}
	if p.challenge == nil || p.challenge.HumanVerificationToken == "" {
		return s.State(), errors.New("this verification method is not supported; cancel and try signing in again")
	}
	solved := p.challenge.HumanVerificationToken
	p.client.SetHumanVerification(solved, strings.Join(p.challenge.HumanVerificationMethods, ","))
	state, err := s.attemptLogin(p)
	if err != nil {
		return state, err
	}
	if state.Stage == "verification" {
		// Proton asked again, so the challenge was not accepted. A retry must
		// not keep sending a token Proton has just rejected.
		p.client.ClearHumanVerification()
		if p.challenge != nil && p.challenge.HumanVerificationToken != solved {
			return state, errors.New("verification was not confirmed and Proton issued a new challenge; open verification again, complete it, then continue")
		}
		return state, errors.New("verification is not complete yet; finish it in your browser, then continue")
	}
	return state, nil
}
