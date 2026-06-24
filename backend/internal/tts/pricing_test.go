package tts

import "testing"

func approx(a, b float64) bool {
	d := a - b
	return d > -1e-12 && d < 1e-12
}

func TestPricing_ActualExact(t *testing.T) {
	if bytesPerAudioToken != 1920 {
		t.Fatalf("bytesPerAudioToken=%d, want 1920 (48000 B/s ÷ 25 tokens/s)", bytesPerAudioToken)
	}
	p := Pricing{USDPer1MAudioTokens: 20, USDPerGBEgress: 0.12}

	// 1920 bytes is exactly one audio token.
	miss := p.ActualUSD(bytesPerAudioToken, true)
	hit := p.ActualUSD(bytesPerAudioToken, false)

	// A hit is egress only, so it must be cheaper than a miss.
	if !(miss > hit) {
		t.Fatalf("miss (%v) should exceed hit (%v)", miss, hit)
	}
	// The synthesis component is exactly one token at $20 / 1M tokens.
	if want := 20.0 / 1e6; !approx(miss-hit, want) {
		t.Fatalf("synthesis component = %v, want %v", miss-hit, want)
	}
	// The hit is exactly the egress of 1920 bytes at $0.12/GB.
	if want := float64(bytesPerAudioToken) / 1e9 * 0.12; !approx(hit, want) {
		t.Fatalf("egress component = %v, want %v", hit, want)
	}
}

func TestPricing_EstimateHasMargin(t *testing.T) {
	p := Pricing{USDPer1MAudioTokens: 20, USDPerGBEgress: 0.12}
	chars := 800
	estBytes := int(float64(chars) / estCharsPerSecond * SampleRate * 2)

	est := p.EstimateUSD(chars)
	actual := p.ActualUSD(estBytes, true)

	// The reservation must exceed the actual cost of the same audio, so
	// settlement only ever refunds (overshoot stays zero).
	if est <= actual {
		t.Fatalf("estimate %v should exceed actual %v", est, actual)
	}
	if r := est / actual; r < 1.4 || r > 1.6 {
		t.Fatalf("estimate/actual ratio = %v, want ~%v", r, estMargin)
	}
}

func TestPricing_ZeroRatesUnmetered(t *testing.T) {
	var p Pricing // both rates zero
	if c := p.ActualUSD(1_000_000, true); c != 0 {
		t.Fatalf("zero rates: ActualUSD = %v, want 0", c)
	}
	if c := p.EstimateUSD(10_000); c != 0 {
		t.Fatalf("zero rates: EstimateUSD = %v, want 0", c)
	}
}
