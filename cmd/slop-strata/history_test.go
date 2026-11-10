package main

import (
	"slices"
	"testing"
)

func TestMarkNoiseFlagsWeeksWithinOneMAD(t *testing.T) {
	t.Parallel()
	res := &historyResult{Weeks: []weekRow{{MassNet: 1}, {MassNet: 2}, {MassNet: 3}, {MassNet: 4}, {MassNet: 100}}}
	markNoise(res)

	if res.NoiseFloor != 1 {
		t.Fatalf("noise floor = %v, want the median absolute deviation 1", res.NoiseFloor)
	}
	got := make([]bool, len(res.Weeks))
	for i, wk := range res.Weeks {
		got[i] = wk.WithinNoise
	}
	if want := []bool{false, true, true, true, false}; !slices.Equal(got, want) {
		t.Fatalf("within noise = %v, want %v", got, want)
	}
}
