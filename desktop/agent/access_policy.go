package main

// access_policy.go — F5 of the Access Layer (see ACCESS_LAYER_HANDOFF.md).
//
// The Policy Guard is consulted BEFORE automating a source so the platform stays on the right
// side of the law and a service's ToS: legitimate access (your own accounts, public data,
// entitled services) is ALLOWED; jurisdiction-illegal or clearly-prohibited actions are BLOCKED
// or WARNED. This boundary is what makes the remote-hands capability shippable rather than a
// circumvention tool. It is deliberately conservative: unknown sources are allowed (we don't
// over-block legitimate use); only well-known sensitive combinations are blocked/warned.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PolicyDecision is the result of evaluating a {source, action, jurisdiction} triple.
type PolicyDecision struct {
	Decision     string `json:"decision"` // "allow" | "warn" | "block"
	Reason       string `json:"reason"`
	Source       string `json:"source"`
	Action       string `json:"action"`
	Jurisdiction string `json:"jurisdiction,omitempty"`
	Category     string `json:"category,omitempty"`
}

// policyRule matches a source by domain substring and, for a category, names the jurisdictions
// where funding/placing actions are illegal. Read/observe ("data") is always allowed.
type policyRule struct {
	Match     []string `json:"match"`      // domain substrings (lowercased compare)
	Category  string   `json:"category"`   // e.g. "gambling"
	BlockedIn []string `json:"blocked_in"` // jurisdiction codes where funding/placing is illegal; "*" = everywhere
	Note      string   `json:"note"`
}

// Built-in rules for the sources this project touched. Extend via ~/.yaver/access-policy.json.
var builtinPolicyRules = []policyRule{
	{Match: []string{"betfair", "bet365", "1xbet", "pinnacle", "williamhill", "unibet", "bwin", "ladbrokes", "betway"},
		Category: "gambling-foreign", BlockedIn: []string{"TR", "US"},
		Note: "foreign sportsbook; betting is illegal from Turkey (only state Iddaa/Misli/Spor Toto are legal) and most US states"},
	{Match: []string{"superbet", "mozzart", "meridian", "maxbet", "admiralbet"},
		Category: "gambling-regional", BlockedIn: []string{"TR"},
		Note: "regionally-licensed book (e.g. RS); betting from Turkey is illegal"},
	{Match: []string{"misli.com", "nesine", "iddaa", "bilyoner", "tuttur"},
		Category: "gambling-licensed-tr", BlockedIn: []string{},
		Note: "Turkey state-licensed operator; betting from Turkey is legal"},
}

// funding/placing actions (the illegal-from-jurisdiction ones); read/data are NOT here.
func isFundingAction(a string) bool {
	switch a {
	case "bet", "place_bet", "placebet", "stake", "deposit", "withdraw", "fund":
		return true
	}
	return false
}
func isAccountAction(a string) bool {
	switch a {
	case "login", "signup", "register", "signin", "account":
		return true
	}
	return false
}

func loadUserPolicyRules() []policyRule {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".yaver", "access-policy.json"))
	if err != nil {
		return nil
	}
	var rules []policyRule
	if json.Unmarshal(b, &rules) != nil {
		return nil
	}
	return rules
}

// EvaluateAccessPolicy decides whether {source, action} is permitted from jurisdiction.
// action examples: "data"/"read"/"observe"/"scrape" (always allowed), "login"/"signup",
// "bet"/"deposit"/"withdraw" (funding). jurisdiction is an ISO-ish code ("TR", "US", "RS"); ""
// = unknown.
func EvaluateAccessPolicy(source, action, jurisdiction string) PolicyDecision {
	src := strings.ToLower(strings.TrimSpace(source))
	act := strings.ToLower(strings.TrimSpace(action))
	jur := strings.ToUpper(strings.TrimSpace(jurisdiction))
	rules := append(loadUserPolicyRules(), builtinPolicyRules...) // user rules win (checked first)

	for _, r := range rules {
		matched := false
		for _, m := range r.Match {
			if m != "" && strings.Contains(src, strings.ToLower(m)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		blockedHere := false
		jurKnown := jur != ""
		for _, j := range r.BlockedIn {
			if j == "*" || (jurKnown && j == jur) {
				blockedHere = true
				break
			}
		}
		switch {
		case isFundingAction(act):
			if blockedHere {
				return PolicyDecision{"block", r.Note + " — placing or funding bets is not permitted from this jurisdiction. Data/observation is allowed.", source, action, jur, r.Category}
			}
			if !jurKnown && len(r.BlockedIn) > 0 {
				return PolicyDecision{"warn", "jurisdiction unknown for a gambling source — confirm betting is legal where you are before funding/placing. " + r.Note, source, action, jur, r.Category}
			}
			return PolicyDecision{"allow", "permitted in this jurisdiction", source, action, jur, r.Category}
		case isAccountAction(act):
			if blockedHere {
				return PolicyDecision{"warn", r.Note + " — an account here may only be used for legitimate non-betting purposes (e.g. reading public data); do NOT place bets from a jurisdiction where it is illegal.", source, action, jur, r.Category}
			}
			return PolicyDecision{"allow", "", source, action, jur, r.Category}
		default: // data / read / observe / scrape / anything non-funding, non-account
			return PolicyDecision{"allow", "data/observation only — public-odds/stats reading is permitted", source, action, jur, r.Category}
		}
	}
	// Unknown source: do not over-block legitimate automation.
	return PolicyDecision{"allow", "no policy rule matched; treat as legitimate access to your own account / public data", source, action, jur, ""}
}
