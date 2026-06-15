package tts

import "github.com/h-hmz/logos/internal/philosopher"

// VoiceForStep resolves the voice and style for the given step role.
// Only debater roles ("a", "b") are speakable; other roles return
// ok=false.
func VoiceForStep(role string, a, b philosopher.Data) (voice, style string, ok bool) {
	switch role {
	case "a":
		return a.TTSVoice, a.TTSStyle, true
	case "b":
		return b.TTSVoice, b.TTSStyle, true
	}
	return "", "", false
}
