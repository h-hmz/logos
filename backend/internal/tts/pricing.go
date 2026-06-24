package tts

// Narration cost model (ADR 0013). Cost is derived from the PCM byte count
// because OpenRouter reports no usage.cost for /audio/speech. The byte count
// gives the exact audio-token count, so a settled charge is exact; only the
// pre-flight reservation is an estimate (it predicts duration from input length).

const (
	// bytesPerAudioToken converts PCM bytes to Gemini audio tokens: 24 kHz ×
	// 16-bit mono is 48000 B/s and Gemini bills 25 audio tokens/s, so one audio
	// token is 1920 bytes (exact).
	bytesPerAudioToken = SampleRate * 2 / 25 // 1920

	// estCharsPerSecond is the speaking-rate heuristic used only to estimate
	// duration from input length before synthesis (~800 chars ≈ 60 s, ADR 0002).
	estCharsPerSecond = 13.0

	// estMargin inflates the pre-flight estimate so the reservation reliably
	// exceeds the actual cost (audio duration varies ±20–30% with prosody, the
	// style directive, and inline tags). With actual ≤ reserved, settlement only
	// ever refunds, so overshoot stays zero (ADR 0013).
	estMargin = 1.5
)

// Pricing holds the per-deployment narration rates (config; ADR 0013). Zero
// rates leave that component unmetered.
type Pricing struct {
	USDPer1MAudioTokens float64
	USDPerGBEgress      float64
}

func (p Pricing) synthUSD(bytes int) float64 {
	return float64(bytes) / bytesPerAudioToken * p.USDPer1MAudioTokens / 1e6
}

func (p Pricing) egressUSD(bytes int) float64 {
	return float64(bytes) / 1e9 * p.USDPerGBEgress
}

// ActualUSD is the exact charge for a delivered narration of the given byte
// count. synthesized adds the synthesis cost (a cache miss); a hit is egress
// only.
func (p Pricing) ActualUSD(bytes int, synthesized bool) float64 {
	c := p.egressUSD(bytes)
	if synthesized {
		c += p.synthUSD(bytes)
	}
	return c
}

// EstimateUSD is the conservative pre-flight reservation for an input of chars
// characters: it assumes a cache miss (synthesis + egress) and applies estMargin
// so the reservation exceeds the eventual ActualUSD (ADR 0013).
func (p Pricing) EstimateUSD(chars int) float64 {
	seconds := float64(chars) / estCharsPerSecond
	bytes := int(seconds * SampleRate * 2) // 48000 B/s
	return p.ActualUSD(bytes, true) * estMargin
}
