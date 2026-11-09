package structure

import (
	"math"
	"slices"
)

// CCLimit is the complexity a function crosses for its mass to count as
// eroded, per the paper's definition.
const CCLimit = 10

// Aggregates is the structure family rolled up over a set of functions, kept
// as separate numbers rather than one blended score.
type Aggregates struct {
	Erosion        float64 `json:"erosion"`
	Mass           float64 `json:"mass"`
	FuncCount      int     `json:"func_count"`
	P90Len         int     `json:"p90_len"`
	MaxLen         int     `json:"max_len"`
	P90Nesting     int     `json:"p90_nesting"`
	MaxNesting     int     `json:"max_nesting"`
	P90Params      int     `json:"p90_params"`
	MaxParams      int     `json:"max_params"`
	P90Cognitive   int     `json:"p90_cognitive"`
	MaxCognitive   int     `json:"max_cognitive"`
	Over50         int     `json:"over_50"`
	Files          int     `json:"files"`
	FilesParsedBad int     `json:"files_parse_error"`
}

// Mass is a function's complexity mass: CC times the square root of its size,
// per the paper.
func Mass(f FuncMetrics) float64 {
	return float64(f.CC) * math.Sqrt(float64(f.SLOC))
}

// p90Rank is the percentile the aggregates report.
const p90Rank = 0.9

// p90 answers the nearest-rank 90th percentile of values, 0 when empty.
func p90(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	idx := int(math.Ceil(p90Rank*float64(len(sorted)))) - 1

	return sorted[max(idx, 0)]
}

// Aggregate rolls a set of files up into the structure family's numbers.
func Aggregate(files []FileMetrics) Aggregates {
	var lens, nestings, params, cognitives []int
	var agg Aggregates
	var totalMass, erodedMass float64

	for _, f := range files {
		agg.Files++
		if f.ParseError {
			agg.FilesParsedBad++
		}
		for _, fn := range f.Funcs {
			lens = append(lens, fn.EndLine-fn.StartLine+1)
			nestings = append(nestings, fn.Nesting)
			params = append(params, fn.Params)
			cognitives = append(cognitives, fn.Cognitive)

			m := Mass(fn)
			totalMass += m
			if fn.CC > CCLimit {
				erodedMass += m
			}
			if fn.EndLine-fn.StartLine+1 > 50 { //nolint:mnd // the documented long-function bar
				agg.Over50++
			}
		}
	}

	agg.FuncCount = len(lens)
	agg.Mass = totalMass
	if totalMass > 0 {
		agg.Erosion = erodedMass / totalMass
	}
	agg.P90Len = p90(lens)
	agg.MaxLen = maxOf(lens)
	agg.P90Nesting = p90(nestings)
	agg.MaxNesting = maxOf(nestings)
	agg.P90Params = p90(params)
	agg.MaxParams = maxOf(params)
	agg.P90Cognitive = p90(cognitives)
	agg.MaxCognitive = maxOf(cognitives)

	return agg
}

func maxOf(values []int) int {
	m := 0
	for _, v := range values {
		if v > m {
			m = v
		}
	}

	return m
}
