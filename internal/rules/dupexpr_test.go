package rules_test

import (
	"strings"
	"testing"
)

// A price worked out in one expression, long enough to be worth a name, and
// the two statements of different shape that hold it.
const (
	goPrice    = "round(o.Net*(1+cfg.Rates[o.Region].VAT)+cfg.Fees[o.Carrier].Flat, cfg.Precision)"
	goTotal    = "package bill\n\nfunc total(o *Order, cfg *Config) float64 {\n\treturn EXPR\n}\n"
	goQuote    = "package bill\n\nfunc quote(o *Order, cfg *Config) string {\n" + goQuoteIf + "\n\treturn \"\"\n}\n"
	goQuoteIf  = "\tif amount := EXPR; amount > cfg.Floor {\n\t\treturn format(amount)\n\t}\n"
	pyPrice    = "round(order.net * (1 + cfg.rates[order.region].vat) + cfg.fees[order.carrier].flat, cfg.precision)"
	pyTotal    = "def total(order, cfg):\n    return EXPR\n"
	pyQuote    = "def quote(order, cfg):\n    amount = EXPR\n    return f\"{amount:.2f}\"\n"
	tsPrice    = "round(order.net * (1 + cfg.rates[order.region].vat) + cfg.fees[order.carrier].flat, cfg.precision)"
	tsTotal    = "export function total(order: Order, cfg: Config): number {\n  return EXPR\n}\n"
	tsQuote    = "export function quote(order: Order, cfg: Config): string {\n  const amount = EXPR\n" + tsQuoteEnd
	tsQuoteEnd = "  return amount.toFixed(2)\n}\n"
)

func withExpr(tmpl, expr string) string { return strings.Replace(tmpl, "EXPR", expr, 1) }

func TestNewDuplicatesReportsARepeatedExpression(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, held, path, added, want string
	}{
		{
			name: "go", held: "bill/total.go:4", path: "bill/quote.go",
			added: withExpr(goQuote, goPrice), want: "36 tokens, identical",
		},
		{
			name: "python", held: "bill/total.py:2", path: "bill/quote.py",
			added: withExpr(pyQuote, pyPrice), want: "36 tokens, identical",
		},
		{
			name: "typescript", held: "bill/total.ts:2", path: "bill/quote.ts",
			added: withExpr(tsQuote, tsPrice), want: "36 tokens, identical",
		},
		{
			name: "one name changed", held: "bill/total.go:4", path: "bill/quote.go",
			added: withExpr(goQuote, strings.Replace(goPrice, "o.Net", "o.Gross", 1)),
			want:  "36 tokens, 1 difference (Net→Gross)",
		},
	}
	trees := map[string]string{
		"bill/total.go": withExpr(goTotal, goPrice), "bill/total.py": withExpr(pyTotal, pyPrice),
		"bill/total.ts": withExpr(tsTotal, tsPrice),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file, _, _ := strings.Cut(tt.held, ":")
			tree := map[string]string{file: trees[file]}
			got := newDuplicates(t, tree, []dupChange{{path: tt.path, after: tt.added}}, true)
			fs := got[tt.path]
			want := "repeats an expression of total at " + tt.held + ", in the same directory: " + tt.want +
				". Give it one name that both places use instead of writing it again"
			if len(fs) != 1 || fs[0].Note != want {
				t.Fatalf("got %+v, want one finding with the note %q", got, want)
			}
		})
	}
}

// TestNewDuplicatesCountsTheCalleeOfARepeatedCall pins that the record of a
// call is the call and not its list of arguments: two functions called with
// the same arguments differ in one name.
func TestNewDuplicatesCountsTheCalleeOfARepeatedCall(t *testing.T) {
	t.Parallel()
	const args = "(o.Net, cfg.Rates[o.Region].Low, cfg.Rates[o.Region].High, cfg.Precision, cfg.Mode)"
	got := newDuplicates(t, map[string]string{"bill/total.go": withExpr(goTotal, "clamp"+args)},
		[]dupChange{{path: "bill/quote.go", after: withExpr(goQuote, "bound"+args)}}, true)
	fs := got["bill/quote.go"]
	if len(fs) != 1 || !strings.Contains(fs[0].Note, "1 difference (clamp→bound)") {
		t.Errorf("got %+v, want one finding that names the function called as the difference", got)
	}
}

// A call that is handed a function: the call's own tokens are the wrapping,
// and the two bodies have nothing in common.
const goRetry = `package bill

func NAME(o *Order, cfg *Config) error {
	return retry(cfg.Attempts, cfg.Backoff, cfg.Jitter, logger.With("op", o.Kind, "id", o.ID), func() error {
		return BODY
	})
}
`

func TestNewDuplicatesLeavesAnExpressionAlone(t *testing.T) {
	t.Parallel()
	total := withExpr(goTotal, goPrice)
	quote := withExpr(goQuote, goPrice)
	retry := func(name, body string) string {
		return strings.NewReplacer("NAME", name, "BODY", body).Replace(goRetry)
	}
	const short = "round(o.Net*cfg.Rate, cfg.Precision)"
	tests := []struct {
		name    string
		tree    map[string]string
		changed []dupChange
	}{
		{
			name:    "too short to score at the floor",
			tree:    map[string]string{"bill/total.go": withExpr(goTotal, short)},
			changed: []dupChange{{path: "bill/quote.go", after: withExpr(goQuote, short)}},
		},
		{
			name: "more names changed than it is worth",
			tree: map[string]string{"bill/total.go": total},
			changed: []dupChange{{path: "bill/quote.go", after: withExpr(goQuote, strings.NewReplacer(
				"Precision", "Digits", "Carrier", "Courier", "Region", "Zone").Replace(goPrice))}},
		},
		{
			name: "twice in one function",
			changed: []dupChange{{path: "bill/quote.go", after: strings.Replace(quote, "\n\treturn \"\"\n",
				"\tlog.Print(\"none\")\n\tlog.Print(\"at all\")\n\n\treturn label("+goPrice+")\n", 1)}},
		},
		{
			name:    "the wrapping of two different function bodies",
			tree:    map[string]string{"bill/load.go": retry("load", "fetch(o)")},
			changed: []dupChange{{path: "bill/save.go", after: retry("save", "store(o, cfg.Table)")}},
		},
		{
			name: "a statement rewritten around an expression it already held",
			tree: map[string]string{"bill/total.go": total},
			changed: []dupChange{{
				path: "bill/quote.go", before: withExpr(goTotal, goPrice+" + 1"), after: quote,
			}},
		},
		{
			name:    "an expression only a test holds",
			tree:    map[string]string{"bill/total_test.go": total},
			changed: []dupChange{{path: "bill/quote.go", after: quote}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := newDuplicates(t, tt.tree, tt.changed, true); len(got) != 0 {
				t.Errorf("got %+v, want no finding", got)
			}
		})
	}
}
