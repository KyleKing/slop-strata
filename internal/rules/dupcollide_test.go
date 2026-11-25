package rules_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// goLedgerLine is a function of one statement that shares no shape with a
// statement of goTally: it holds more tokens and more names than any of them.
const goLedgerLine = `package ledger

func post(l *Ledger, e Entry) error {
	return l.journal.Append(e.Account, e.Debit, e.Credit, e.Currency, e.Memo, e.PostedAt, e.Batch, e.Source)
}
`

// shapeTable answers where a fingerprint's packed statement shape hashes
// start and how many there are, as Fragments documents the layout: a count,
// then four bytes a hash.
func shapeTable(t *testing.T, fp *rules.Fingerprint) (int, int) {
	t.Helper()
	count, used := binary.Uvarint(fp.Frags)
	if used <= 0 || count == 0 {
		t.Fatalf("no statement table in %d bytes", len(fp.Frags))
	}

	return used, int(count) //nolint:gosec // a handful of statements
}

// shapeHashes lists a fingerprint's statement shape hashes.
func shapeHashes(t *testing.T, fp *rules.Fingerprint) []uint32 {
	t.Helper()
	start, count := shapeTable(t, fp)
	out := make([]uint32, 0, count)
	for i := range count {
		out = append(out, binary.LittleEndian.Uint32(fp.Frags[start+4*i:]))
	}

	return out
}

// withShape answers a copy of a fingerprint whose every statement carries one
// shape hash: what a hash collision looks like to the duplicate pass, which
// meets statements of that hash and finds they hold other tokens and slots.
func withShape(t *testing.T, fp *rules.Fingerprint, shape uint32) *rules.Fingerprint {
	t.Helper()
	out := roundTrip(t, fp)
	start, count := shapeTable(t, out)
	for i := range count {
		binary.LittleEndian.PutUint32(out.Frags[start+4*i:], shape)
	}

	return out
}

// TestDuplicatesTolerateAShapeHashCollision gives a statement the shape hash
// of each statement of a copied function in turn, though it holds other
// tokens and another number of slots. Two statements of one hash are a seed,
// and the pass must check that a seed lines up before it reads the two slot
// for slot: the copy is still reported, and the collision is not, on either
// side of a change and in the snapshot's groups.
func TestDuplicatesTolerateAShapeHashCollision(t *testing.T) {
	t.Parallel()
	users := printOf(t, "store/users.go", tallySrc(goTally, "parseUsers", "users"))
	orders := printOf(t, "store/orders.go", tallySrc(goTally, "parseOrders", "orders"))
	ledger := printOf(t, "ledger/post.go", goLedgerLine)
	added := func(int) bool { return true }
	for i, shape := range shapeHashes(t, users) {
		tree := map[string]*rules.Fingerprint{
			"store/users.go": users, "store/orders.go": orders, "ledger/post.go": withShape(t, ledger, shape),
		}
		groups := rules.DuplicateGroups(tree)
		if len(groups) != 1 || sitePaths(&groups[0]) != "store/orders.go parseOrders, store/users.go parseUsers" {
			t.Errorf("statement %d: got groups %+v, want the one copy", i, groups)
		}
		for _, path := range []string{"store/orders.go", "ledger/post.go"} {
			got := rules.NewDuplicates(tree, []rules.DupChange{{Path: path, After: tree[path], Added: added}}, true)
			copied := got["store/orders.go"]
			if len(got["ledger/post.go"]) != 0 || (path == "store/orders.go") != (len(copied) == 1) ||
				(len(copied) == 1 && !strings.Contains(copied[0].Note, "repeats parseUsers at store/users.go")) {
				t.Errorf("statement %d, change to %s: got %+v, want only the copy reported", i, path, got)
			}
		}
	}
}
