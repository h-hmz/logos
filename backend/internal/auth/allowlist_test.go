package auth

import "testing"

func TestParseAllowlist_Allows(t *testing.T) {
	verified := func(email string) Identity { return Identity{Email: email, EmailVerified: true} }

	cases := []struct {
		name string
		spec string
		id   Identity
		want bool
	}{
		{"domain match", "example.com", verified("a@example.com"), true},
		{"domain miss", "example.com", verified("a@other.com"), false},
		{"full email match", "alice@example.com", verified("alice@example.com"), true},
		{"full email is exact", "alice@example.com", verified("bob@example.com"), false},
		{"leading @ on domain", "@example.com", verified("a@example.com"), true},
		{"whitespace and case", "  Example.COM , Alice@Other.com ", verified("A@EXAMPLE.com"), true},
		{"second entry matches", "example.com,bob@other.com", verified("bob@other.com"), true},
		{"unverified denied", "example.com", Identity{Email: "a@example.com"}, false},
		{"empty email denied", "example.com", verified(""), false},
		{"empty spec denies all", "", verified("a@example.com"), false},
		{"wildcard allows unverified", "*", Identity{Email: "x@y.com"}, true},
		{"wildcard allows empty email", "*", Identity{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseAllowlist(tc.spec).Allows(tc.id); got != tc.want {
				t.Fatalf("Allows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseAllowlist_EmptyAndOpen(t *testing.T) {
	if a := ParseAllowlist(""); !a.Empty() || a.Open() {
		t.Fatalf("empty spec: Empty=%v Open=%v, want true/false", a.Empty(), a.Open())
	}
	if a := ParseAllowlist("  ,  "); !a.Empty() {
		t.Fatalf("whitespace-only spec should be Empty")
	}
	if a := ParseAllowlist("*"); a.Empty() || !a.Open() {
		t.Fatalf("wildcard: Empty=%v Open=%v, want false/true", a.Empty(), a.Open())
	}
	if a := ParseAllowlist("example.com"); a.Empty() || a.Open() {
		t.Fatalf("domain spec: Empty=%v Open=%v, want false/false", a.Empty(), a.Open())
	}
}
