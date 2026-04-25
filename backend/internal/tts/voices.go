package tts

import "github.com/h-hmz/logos/internal/philosopher"

// PhilosopherVoice returns the Gemini voice for d, falling back to
// DefaultGeminiVoice when d.TTSVoice is empty.
func PhilosopherVoice(d philosopher.Data) string {
	if d.TTSVoice != "" {
		return d.TTSVoice
	}
	return DefaultGeminiVoice
}

// VoiceForStep resolves the voice and style for the given step role.
// Only debater roles ("a", "b") are speakable; other roles return
// ok=false.
func VoiceForStep(role string, a, b philosopher.Data) (voice, style string, ok bool) {
	switch role {
	case "a":
		return PhilosopherVoice(a), a.TTSStyle, true
	case "b":
		return PhilosopherVoice(b), b.TTSStyle, true
	}
	return "", "", false
}
