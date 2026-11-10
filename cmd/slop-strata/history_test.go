package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestHotspotTableRanksByScore(t *testing.T) {
	t.Parallel()
	res := &historyResult{Hotspots: map[string]float64{"a": 5353, "b": 35565, "c": 35565, "d": 9}}
	var out bytes.Buffer
	printHotspots(&out, res)

	var order []string
	for _, line := range strings.Split(out.String(), "\n") {
		name, _, _ := strings.Cut(line, " ")
		if slices.Contains([]string{"a", "b", "c", "d"}, name) {
			order = append(order, name)
		}
	}
	if want := []string{"b", "c", "a", "d"}; !slices.Equal(order, want) {
		t.Fatalf("hotspot order = %v, want %v (score descending, then name)", order, want)
	}
}

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
