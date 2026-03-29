package philosopher

import "testing"

func TestKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"John Stuart Mill", "john_stuart_mill"},
		{"Al-Ghazali", "al_ghazali"},
		{"Simone de Beauvoir", "simone_de_beauvoir"},
		{"Ibn Rushd", "ibn_rushd"},
		{"Nagarjuna", "nagarjuna"},
		{"Marcus Aurelius", "marcus_aurelius"},
		{"Thomas Aquinas", "thomas_aquinas"},
		{"Karl Marx", "karl_marx"},
	}
	for _, tt := range tests {
		got := Key(tt.input)
		if got != tt.want {
			t.Errorf("Key(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
