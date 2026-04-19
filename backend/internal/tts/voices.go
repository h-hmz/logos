package tts

import (
	"strings"

	"github.com/h-hmz/logos/internal/config"
	"github.com/h-hmz/logos/internal/philosopher"
)

// Non-debater role voices, keyed by provider then role. The "gemini" entry
// is the baseline fallback when the active provider has no override for a
// given role.
var roleVoices = map[string]map[string]string{
	"gemini": {
		"moderator": "Charon",
		"critic":    "Puck",
		"judge":     "Orus",
		"fallback":  "Kore",
	},
	"elevenlabs": {
		"moderator": "JBFqnCBsd6RMkjVDRZzb", // George — warm, authoritative
		"critic":    "onwK4e9ZLuTAKqWW03F9", // Daniel — sharp, incisive
		"judge":     "nPczCjzI2devNBz1zQrb", // Brian — measured, grave
		"fallback":  "pNInz6obpgDQGcFmaJgB", // Adam — neutral default
	},
	"openai": {
		"moderator": "fable",
		"critic":    "ash",
		"judge":     "onyx",
		"fallback":  "alloy",
	},
}

// RoleVoice returns the voice ID for a non-debater role ("moderator",
// "critic", "judge", or "fallback") under the given provider. Falls back to
// the Gemini mapping when the provider has no override.
func RoleVoice(provider, role string) string {
	if m, ok := roleVoices[provider]; ok {
		if v := m[role]; v != "" {
			return v
		}
	}
	return roleVoices["gemini"][role]
}

// PhilosopherVoice picks the provider-specific voice from d.TTSVoices, or
// falls back to the provider's role fallback voice when no entry exists.
func PhilosopherVoice(provider string, d philosopher.Data) string {
	if v, ok := d.TTSVoices[provider]; ok && v != "" {
		return v
	}
	return RoleVoice(provider, "fallback")
}

// VoiceForStep resolves the TTS voice and style for the given step role,
// using the currently configured provider. Returns ("", "", false) when the
// step should not be synthesized.
func VoiceForStep(role string, a, b philosopher.Data) (voice, style string, ok bool) {
	provider := strings.ToLower(config.TTSProvider())
	switch role {
	case "a":
		return PhilosopherVoice(provider, a), a.TTSStyle, true
	case "b":
		return PhilosopherVoice(provider, b), b.TTSStyle, true
	case "moderator":
		return RoleVoice(provider, "moderator"), "Speak with gravitas, like a seasoned debate moderator.", true
	case "critic":
		return RoleVoice(provider, "critic"), "Speak incisively, like a sharp academic critic.", true
	case "judge":
		return RoleVoice(provider, "judge"), "Speak with measured, judicial authority.", true
	}
	return "", "", false
}
