package terminal

import "testing"

func TestModeForDimensions(t *testing.T) {
	tests := []struct {
		name string
		cols int
		rows int
		want SizeMode
	}{
		{"micro by width", 39, 24, SizeModeMicro},
		{"micro by height", 80, 9, SizeModeMicro},
		{"minimal", 40, 15, SizeModeMinimal},
		{"compact", 60, 23, SizeModeCompact},
		{"standard", 80, 24, SizeModeStandard},
		{"wide", 120, 40, SizeModeWide},
		{"ultrawide", 200, 60, SizeModeUltrawide},
		{"massive", 400, 80, SizeModeMassive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ModeForDimensions(test.cols, test.rows); got != test.want {
				t.Fatalf("ModeForDimensions(%d, %d) = %v, want %v", test.cols, test.rows, got, test.want)
			}
		})
	}
}

func TestSizeForDimensionsAppliesFallbacks(t *testing.T) {
	got := SizeForDimensions(0, 0)
	if got.Cols != 80 || got.Rows != 24 || got.Mode != SizeModeStandard {
		t.Fatalf("SizeForDimensions(0, 0) = %#v", got)
	}
}
