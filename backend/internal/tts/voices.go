package tts

import (
	"strings"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
)

// fallbackVoices hold the provider-specific voice used when a debater's YAML
// has no entry for the active provider. Only debater turns are synthesized;
// moderator/critic/judge turns are text-only.
var fallbackVoices = map[string]string{
	"gemini":     "Kore",
	"elevenlabs": "pNInz6obpgDQGcFmaJgB", // Adam — neutral default
	"openai":     "alloy",
}

// PhilosopherVoice picks the provider-specific voice from d.TTSVoices, or
// falls back to the provider's neutral default when no entry exists.
func PhilosopherVoice(provider string, d philosopher.Data) string {
	if v, ok := d.TTSVoices[provider]; ok && v != "" {
		return v
	}
	if v, ok := fallbackVoices[provider]; ok {
		return v
	}
	return fallbackVoices["gemini"]
}

// VoiceForStep resolves the TTS voice and style for the given step role,
// using the currently configured provider. Only debater roles ("a", "b") are
// speakable; everything else returns ok=false.
func VoiceForStep(role string, a, b philosopher.Data) (voice, style string, ok bool) {
	provider := strings.ToLower(config.TTSProvider())
	switch role {
	case "a":
		return PhilosopherVoice(provider, a), a.TTSStyle, true
	case "b":
		return PhilosopherVoice(provider, b), b.TTSStyle, true
	}
	return "", "", false
}
