package auth

import "strings"

// Allowlist authorizes verified callers by email. Entries are either full
// email addresses ("alice@example.com", admitting exactly that address) or
// bare domains ("example.com", admitting any address at that domain). Matching
// is case-insensitive. The special entry "*" admits every verified caller and
// is intended as an explicit escape hatch for local development.
//
// An Allowlist with no entries denies everyone (fail-closed). Build one from a
// configured spec via ParseAllowlist; the zero value also denies everyone.
type Allowlist struct {
	emails  map[string]struct{}
	domains map[string]struct{}
	all     bool
}

// ParseAllowlist builds an Allowlist from a comma-separated spec (e.g.
// "example.com,alice@other.com"). Entries containing "@" are treated as full
// email addresses; all others as domains (a leading "@" is tolerated). The
// single token "*" enables allow-all. Whitespace and case are normalized;
// empty entries are ignored, so an empty spec yields a deny-all Allowlist.
func ParseAllowlist(spec string) Allowlist {
	a := Allowlist{
		emails:  make(map[string]struct{}),
		domains: make(map[string]struct{}),
	}
	for _, raw := range strings.Split(spec, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case entry == "":
			continue
		case entry == "*":
			a.all = true
		case strings.HasPrefix(entry, "@"):
			// "@example.com" is a domain shorthand, not an address.
			a.domains[entry[1:]] = struct{}{}
		case strings.Contains(entry, "@"):
			a.emails[entry] = struct{}{}
		default:
			a.domains[entry] = struct{}{}
		}
	}
	return a
}

// Allows reports whether id may access protected routes. Unless the allowlist
// is allow-all, the caller must present a verified, non-empty email: an empty
// or unverified email is always denied. A full-email entry matches the address
// exactly; a domain entry matches the part after the final "@".
func (a Allowlist) Allows(id Identity) bool {
	if a.all {
		return true
	}
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if email == "" || !id.EmailVerified {
		return false
	}
	if _, ok := a.emails[email]; ok {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	_, ok := a.domains[email[at+1:]]
	return ok
}

// Open reports whether the allowlist admits all callers (the "*" entry).
func (a Allowlist) Open() bool { return a.all }

// Empty reports whether no entries are configured, in which case the allowlist
// denies everyone. Callers use this to warn at startup.
func (a Allowlist) Empty() bool {
	return !a.all && len(a.emails) == 0 && len(a.domains) == 0
}
