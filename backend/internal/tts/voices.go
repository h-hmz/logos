package tts

import (
	"strings"

	"github.com/h-hmz/logos/internal/philosopher"
)

// VoiceForStepKey resolves the voice and style for a step from its key alone,
// matching the philosopher slug that prefixes a debater step key (e.g.
// "socrates_r1") against the catalog.
func VoiceForStepKey(stepKey string, catalog []philosopher.Data) (voice, style string, ok bool) {
	best := ""
	for _, p := range catalog {
		slug := philosopher.Key(p.Name)
		if strings.HasPrefix(stepKey, slug+"_") && len(slug) > len(best) {
			best, voice, style, ok = slug, p.TTSVoice, p.TTSStyle, true
		}
	}
	return voice, style, ok
}
