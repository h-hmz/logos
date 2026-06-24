package tts

import (
	"testing"

	"github.com/h-hmz/logos/internal/philosopher"
)

func TestVoiceForStepKey(t *testing.T) {
	// Slugs: "socrates", "jean_jacques_rousseau" (multi-underscore).
	catalog := []philosopher.Data{
		{Name: "Socrates", TTSVoice: "Kore", TTSStyle: "wry"},
		{Name: "Jean-Jacques Rousseau", TTSVoice: "Puck", TTSStyle: "warm"},
	}

	cases := []struct {
		stepKey   string
		wantVoice string
		wantOK    bool
	}{
		{"socrates_r1", "Kore", true},
		{"socrates_r2", "Kore", true},
		{"jean_jacques_rousseau_r1", "Puck", true},
		{"jean_jacques_rousseau_critic_response", "Puck", true},
		{"moderator_intro", "", false}, // referee steps are not speakable
		{"critic_feedback", "", false},
		{"final_verdict", "", false},
		{"plato_r1", "", false},        // philosopher not in the catalog
		{"socrates", "", false},        // bare slug, no "_" suffix → not a step key
	}
	for _, c := range cases {
		voice, _, ok := VoiceForStepKey(c.stepKey, catalog)
		if ok != c.wantOK || voice != c.wantVoice {
			t.Errorf("VoiceForStepKey(%q) = (%q, %v), want (%q, %v)", c.stepKey, voice, ok, c.wantVoice, c.wantOK)
		}
	}
}

// One philosopher's slug being a prefix of another's must resolve to the longest
// match, not the first.
func TestVoiceForStepKey_LongestMatch(t *testing.T) {
	catalog := []philosopher.Data{
		{Name: "Kant", TTSVoice: "VoiceA"},    // slug "kant"
		{Name: "Kant Jr", TTSVoice: "VoiceB"}, // slug "kant_jr"
	}
	if v, _, ok := VoiceForStepKey("kant_jr_r1", catalog); !ok || v != "VoiceB" {
		t.Errorf("kant_jr_r1 = (%q, %v), want (VoiceB, true)", v, ok)
	}
	if v, _, ok := VoiceForStepKey("kant_r1", catalog); !ok || v != "VoiceA" {
		t.Errorf("kant_r1 = (%q, %v), want (VoiceA, true)", v, ok)
	}
}
