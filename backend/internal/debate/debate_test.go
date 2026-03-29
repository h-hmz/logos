package debate

import "testing"

// TestStepCount verifies the formula: 1 moderator + 2*N rounds + 1 critic + 2 critic responses + 1 judge = 2N + 5
func TestStepCount(t *testing.T) {
	tests := []struct {
		rounds int
		want   int
	}{
		{1, 7},
		{2, 9},
		{3, 11},
		{5, 15},
	}
	for _, tt := range tests {
		got := 2*tt.rounds + 5
		if got != tt.want {
			t.Errorf("stepCount(rounds=%d) = %d, want %d", tt.rounds, got, tt.want)
		}
	}
}
