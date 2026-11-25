package rules_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kyleking/slop-strata/internal/rules"
)

// A store written per language: two members of six or seven statements, with
// NAME for the container and TABLE for the one literal it differs in. EXTRA
// is a line a case adds to the first member.
const (
	pyStore = `class NAME(Base):
    def load(self, key):
        rows = self.db.query("select * from TABLE where id = ?", key)
        if not rows:
            raise KeyError(key)
        result = [self.convert(row) for row in rows]
        self.cache[key] = result
EXTRA        return result

    def save(self, key, value):
        payload = self.encode(value)
        if payload is None:
            raise ValueError(value)
        self.db.execute("update TABLE set body = ? where id = ?", payload, key)
        self.cache[key] = value
        return payload
`
	goStore = `package store

type NAME struct{ db *DB }

func (s *NAME) Load(key string) ([]Row, error) {
	rows, err := s.db.Query("select * from TABLE where id = ?", key)
	if err != nil {
		return nil, fmt.Errorf("loading: %w", err)
	}
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		out = append(out, convert(row))
	}
EXTRA	return out, nil
}

func (s *NAME) Save(key string, value Row) error {
	payload, err := encode(value)
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	if err := s.db.Exec("update TABLE set body = ? where id = ?", payload, key); err != nil {
		return fmt.Errorf("saving: %w", err)
	}
	s.cache[key] = value
	return nil
}
`
	tsStore = `export class NAME extends Base {
  async load(key: string): Promise<Row[]> {
    const rows = await this.db.query("select * from TABLE where id = ?", key)
    if (rows.length === 0) {
      throw new NotFound(key)
    }
    const result = rows.map((row) => this.convert(row))
    this.cache.set(key, result)
EXTRA    return result
  }

  async save(key: string, value: Row): Promise<string> {
    const payload = this.encode(value)
    if (payload === null) {
      throw new Invalid(value)
    }
    await this.db.execute("update TABLE set body = ? where id = ?", payload, key)
    this.cache.set(key, value)
    return payload
  }
}
`
	tsModule = `export async function load(db: DB, key: string): Promise<Row[]> {
  const rows = await db.query("select * from TABLE where id = ?", key)
  if (rows.length === 0) {
    throw new NotFound(key)
  }
  const result = rows.map((row) => convert(row))
  cache.set(key, result)
EXTRA  return result
}

export const save = async (db: DB, key: string, value: Row): Promise<string> => {
  const payload = encode(value)
  if (payload === null) {
    throw new Invalid(value)
  }
  await db.execute("update TABLE set body = ? where id = ?", payload, key)
  cache.set(key, value)
  return payload
}
`
)

// store writes one store of a template under a name, with nothing extra.
func store(template, name, table string) string {
	return storeWith(template, name, table, "")
}

// storeWith writes a store whose first member holds one more line.
func storeWith(template, name, table, extra string) string {
	return strings.NewReplacer("NAME", name, "TABLE", table, "EXTRA", extra).Replace(template)
}

// siblingSummary renders sets as kind, sites, members, and counts. Tokens are
// left out: TestSiblingRankIsTokensTimesSitesLessOne reads them.
func siblingSummary(sets []rules.SiblingSet) []string {
	out := make([]string, 0, len(sets))
	for i := range sets {
		s := &sets[i]
		sites := make([]string, 0, len(s.Sites))
		for _, site := range s.Sites {
			sites = append(sites,
				fmt.Sprintf("%s:%d-%d %s +%d", site.Path, site.Line, site.EndLine, site.Name, site.Others))
		}
		out = append(out, fmt.Sprintf("%s | %s | %s | statements %d differing %d",
			s.Kind, strings.Join(sites, ", "), strings.Join(s.Members, ","), s.Statements, s.Differing))
	}

	return out
}

func TestSiblingSetsListContainersWrittenInParallel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tree map[string]string
		name string
		want []string
	}{
		{
			name: "python: two classes with the same two methods",
			tree: map[string]string{
				"store/users.py":  store(pyStore, "UserStore", "users"),
				"store/orders.py": store(pyStore, "OrderStore", "orders"),
			},
			want: []string{
				"class | store/orders.py:1-16 OrderStore +0, store/users.py:1-16 UserStore +0 | load,save | " +
					"statements 12 differing 0",
			},
		},
		{
			name: "go: two types with the same two methods",
			tree: map[string]string{
				"store/users.go":  store(goStore, "Users", "users"),
				"store/orders.go": store(goStore, "Orders", "orders"),
			},
			want: []string{
				"type | store/orders.go:3-27 Orders +0, store/users.go:3-27 Users +0 | Load,Save | " +
					"statements 14 differing 0",
			},
		},
		{
			name: "typescript: two classes, one in a .tsx file",
			tree: map[string]string{
				"store/users.ts":   store(tsStore, "UserStore", "users"),
				"store/orders.tsx": store(tsStore, "OrderStore", "orders"),
			},
			want: []string{
				"class | store/orders.tsx:1-21 OrderStore +0, store/users.ts:1-21 UserStore +0 | load,save | " +
					"statements 12 differing 0",
			},
		},
		{
			name: "typescript: two modules, a function and an arrow function each",
			tree: map[string]string{
				"store/users.ts":  store(tsModule, "", "users"),
				"store/orders.ts": store(tsModule, "", "orders"),
			},
			want: []string{
				"module | store/orders.ts:1-19 orders +0, store/users.ts:1-19 users +0 | load,save | " +
					"statements 12 differing 0",
			},
		},
		{
			name: "three sites, one with a statement of its own and a method of its own",
			tree: map[string]string{
				"store/users.py":  store(pyStore, "UserStore", "users"),
				"store/orders.py": store(pyStore, "OrderStore", "orders"),
				"store/teams.py": storeWith(pyStore, "TeamStore", "teams",
					"        self.audit.record(key, len(result))\n") +
					"\n    def purge(self, key):\n        self.db.execute(\"delete from teams where id = ?\", key)\n" +
					"        self.cache.pop(key, None)\n        self.audit.record(key, 0)\n        return key\n",
			},
			want: []string{
				"class | store/orders.py:1-16 OrderStore +0, store/teams.py:1-23 TeamStore +1, " +
					"store/users.py:1-16 UserStore +0 | load,save | statements 12 differing 1",
			},
		},
		{
			name: "two sets, the one of three sites first",
			tree: map[string]string{
				"store/users.py":  store(pyStore, "UserStore", "users"),
				"store/orders.py": store(pyStore, "OrderStore", "orders"),
				"store/teams.py":  store(pyStore, "TeamStore", "teams"),
				"repo/users.go":   store(goStore, "Users", "users"),
				"repo/orders.go":  store(goStore, "Orders", "orders"),
			},
			want: []string{
				"class | store/orders.py:1-16 OrderStore +0, store/teams.py:1-16 TeamStore +0, " +
					"store/users.py:1-16 UserStore +0 | load,save | statements 12 differing 0",
				"type | repo/orders.go:3-27 Orders +0, repo/users.go:3-27 Users +0 | Load,Save | " +
					"statements 14 differing 0",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := printsOf(t, tc.tree)
			got := siblingSummary(rules.SiblingSets(tree))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sets:\n got %q\nwant %q", got, tc.want)
			}
			if again := siblingSummary(rules.SiblingSets(tree)); !reflect.DeepEqual(again, got) {
				t.Errorf("a second run lists %q, the first %q", again, got)
			}
		})
	}
}

func TestSiblingSetsLeaveOutWhatIsNoSibling(t *testing.T) {
	t.Parallel()
	users, orders := store(pyStore, "UserStore", "users"), store(pyStore, "OrderStore", "orders")
	accessors := "class NAME:\n    def name(self):\n        return self._name\n\n" +
		"    def size(self):\n        return len(self._rows)\n\n    def rows(self):\n        return list(self._rows)\n"
	other := `class OrderStore(Base):
    def load(self, key):
        for attempt in range(3):
            with self.lock:
                if key in self.cache:
                    return self.cache[key]
        return self.fetch(key)

    def save(self, key, value):
        while not self.ready():
            self.wait(timeout=1)
        return self.queue.put((key, value))
`
	cases := []struct {
		tree map[string]string
		name string
	}{
		{name: "one class alone", tree: map[string]string{"store/users.py": users}},
		{name: "the same method names over other bodies", tree: map[string]string{
			"store/users.py": users, "store/orders.py": other,
		}},
		{name: "one member in line and the other of its own name", tree: map[string]string{
			"store/users.py": users, "store/orders.py": strings.Replace(orders, "def save", "def store", 1),
		}},
		{name: "members too short to count", tree: map[string]string{
			"store/users.py":  strings.Replace(accessors, "NAME", "Users", 1),
			"store/orders.py": strings.Replace(accessors, "NAME", "Orders", 1),
		}},
		{name: "the copy is a test", tree: map[string]string{
			"store/users.py": users, "store/test_orders.py": orders,
		}},
		{name: "the copy is an example", tree: map[string]string{
			"store/users.py": users, "examples/orders.py": orders,
		}},
		{name: "the copy is a migration", tree: map[string]string{
			"store/users.py": users, "migrations/orders.py": orders,
		}},
		{name: "a sync and an async flavor of one name", tree: map[string]string{
			"store/client.py": store(pyStore, "Client", "users"),
			"store/aio.py":    store(pyStore, "AsyncClient", "orders"),
		}},
		{name: "two modules of one name in one directory", tree: map[string]string{
			"store/users.ts": store(tsModule, "", "users"), "store/users.tsx": store(tsModule, "", "orders"),
		}},
		{name: "two files of one name kept in step", tree: map[string]string{
			"v1/users.py": users, "v2/users.py": strings.Replace(users, "UserStore", "UserStoreV2", 1),
		}},
		{name: "a class and a module of another language", tree: map[string]string{
			"store/users.py": users, "store/orders.ts": store(tsStore, "OrderStore", "orders"),
		}},
		{name: "a class and a module with the same functions", tree: map[string]string{
			"store/users.ts": store(tsStore, "UserStore", "users"), "store/orders.ts": store(tsModule, "", "orders"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := siblingSummary(rules.SiblingSets(printsOf(t, tc.tree))); len(got) != 0 {
				t.Errorf("sets = %q, want none", got)
			}
		})
	}
}

// TestSiblingRankIsTokensTimesSitesLessOne pins the formula --explain gives:
// per shared member, the tokens in line times one less than the sites that
// hold it.
func TestSiblingRankIsTokensTimesSitesLessOne(t *testing.T) {
	t.Parallel()
	two := map[string]string{
		"store/users.py":  store(pyStore, "UserStore", "users"),
		"store/orders.py": store(pyStore, "OrderStore", "orders"),
	}
	three := map[string]string{"store/teams.py": store(pyStore, "TeamStore", "teams")}
	for name, body := range two {
		three[name] = body
	}
	pair, triple := rules.SiblingSets(printsOf(t, two)), rules.SiblingSets(printsOf(t, three))
	if len(pair) != 1 || len(triple) != 1 {
		t.Fatalf("sets = %d and %d, want one each", len(pair), len(triple))
	}
	if pair[0].Tokens < 60 || pair[0].Rank != pair[0].Tokens {
		t.Errorf("two sites: tokens %d rank %d, want the rank to be the tokens", pair[0].Tokens, pair[0].Rank)
	}
	if triple[0].Tokens != pair[0].Tokens || triple[0].Rank != 2*triple[0].Tokens {
		t.Errorf("three sites: tokens %d rank %d, want the pair's %d tokens and twice that",
			triple[0].Tokens, triple[0].Rank, pair[0].Tokens)
	}
}

// TestSiblingSetsShareAMemberHalfTheSitesHold pins which members a set
// names: one that is in line at half the sites, counted over those sites
// alone.
func TestSiblingSetsShareAMemberHalfTheSitesHold(t *testing.T) {
	t.Parallel()
	purge := "\n    def purge(self, key):\n        self.db.execute(\"delete from TABLE where id = ?\", key)\n" +
		"        self.cache.pop(key, None)\n        self.audit.record(key, 0)\n        return key\n"
	with := func(name, table string) string {
		return store(pyStore, name, table) + strings.ReplaceAll(purge, "TABLE", table)
	}
	tree := map[string]string{
		"store/a.py": with("AStore", "a"), "store/b.py": with("BStore", "b"),
		"store/c.py": store(pyStore, "CStore", "c"), "store/d.py": store(pyStore, "DStore", "d"),
	}
	sets := rules.SiblingSets(printsOf(t, tree))
	if len(sets) != 1 {
		t.Fatalf("sets = %q, want one", siblingSummary(sets))
	}
	s := sets[0]
	if got := strings.Join(s.Members, ","); got != "load,purge,save" {
		t.Errorf("members = %s, want load,purge,save: purge is in line at two of four sites", got)
	}
	four := rules.SiblingSets(printsOf(t, map[string]string{
		"store/a.py": store(pyStore, "AStore", "a"), "store/b.py": store(pyStore, "BStore", "b"),
		"store/c.py": store(pyStore, "CStore", "c"), "store/d.py": store(pyStore, "DStore", "d"),
	}))[0]
	// load and save at four sites, purge at two.
	if want := four.Rank + (s.Tokens - four.Tokens); s.Rank != want {
		t.Errorf("rank = %d, want %d: purge's tokens once on top of the four stores' rank", s.Rank, want)
	}
}
