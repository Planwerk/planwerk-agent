package claude

import "testing"

func TestClampScore(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"below range clamps to 0", -1, 0},
		{"zero stays zero", 0, 0},
		{"mid stays", 5, 5},
		{"max stays", 10, 10},
		{"just above clamps to 10", 11, 10},
		{"far above clamps to 10", 100, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampScore(tc.in); got != tc.want {
				t.Errorf("clampScore(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
