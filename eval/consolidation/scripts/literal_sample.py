"""literal_sample.py OUT sample|count [LABELS] : draw the literal groups that were labeled, or count their labels.

OUT holds one `<repository>.json` per repository of repos.tsv: what `slop-strata snapshot --experimental --json`
printed for it, from a binary built with literalGroupLimit lifted (README.md says how), so every rank is there.
`sample` prints the first five groups of each repository and the draw from the ranks below, with the numbers of
each. `count LABELS` prints the tables DESIGN.md quotes from labels/dup-literals.tsv, and checks that every row
names the value of the group at its rank."""
import collections, json, random, sys

FIT = 'bubbletea chi cobra flask fzf httpx pydantic query rich trpc vite zod documenso dub umami'.split()
FRESH = 'click date-fns echo gin gorm requests starlette swr werkzeug zustand excalidraw formbricks plane'.split()
DRAW = {'fit': 10, 'fresh': 16}
SEED = 20261125
LANG = {'gin': 'go', 'gorm': 'go', 'httpx': 'py', 'requests': 'py', 'starlette': 'py', 'werkzeug': 'py',
        'plane': 'ts and py'}
OUT, MODE = sys.argv[1], sys.argv[2]
cache = {}
def groups(repo):
    if repo not in cache:
        cache[repo] = json.load(open('%s/%s.json' % (OUT, repo)))['result']['literal_groups']['groups']
    return cache[repo]
def drawn(which):
    top, rest = [], []
    for repo in FIT if which == 'fit' else FRESH:
        for i in range(len(groups(repo))):
            (top if i < 5 else rest).append((repo, i + 1))
    random.Random(SEED).shuffle(rest)
    return top, sorted(rest[:DRAW[which]]), len(rest)
def sample():
    for which in ('fit', 'fresh'):
        top, draw, below = drawn(which)
        print('# %s: %d in a top five, %d drawn of %d below' % (which, len(top), len(draw), below))
        for kind, rows in (('top', top), ('draw', draw)):
            for repo, rank in rows:
                g = groups(repo)[rank - 1]
                c = g.get('constant')
                print('\t'.join([repo, which, kind, str(rank), json.dumps(g['value']), 'rank %s' % g['rank'],
                                 'files %d' % g['files'], 'written %d' % g['written'], 'uses %d' % g['uses'],
                                 '%s at %s:%d' % (c['name'], c['path'], c['line']) if c else '-']))
def count(labels):
    rows = [l.rstrip('\n').split('\t') for l in open(labels)]
    want = {(repo, which, kind, rank) for which in ('fit', 'fresh')
            for kind, lst in zip(('top', 'draw'), drawn(which)[:2]) for repo, rank in lst}
    got = {(r[0], r[1], r[2], int(r[3])) for r in rows}
    assert got == want, (sorted(got - want), sorted(want - got))
    tables = collections.defaultdict(lambda: collections.defaultdict(collections.Counter))
    for repo, which, kind, rank, verdict, note in rows:
        g = groups(repo)[int(rank) - 1]
        assert note.startswith('"%s"' % g['value']), (repo, rank, g['value'])
        named = 'a constant names it' if g.get('constant') else 'tested, no constant'
        for table, key in (('group', named), ('group', 'all'), ('draw', kind), ('language', LANG.get(repo, 'ts'))):
            tables[table][(which, key)][verdict] += 1
    for table in ('group', 'draw', 'language'):
        print('by %s' % table)
        for key in sorted(tables[table]):
            c = tables[table][key]
            n = sum(c.values())
            print('  %-5s %-22s read %2d  yes %2d  plausible %2d  no %2d  %3.0f%%  %3.0f%%' % (
                key[0], key[1], n, c['yes'], c['plausible'], c['no'], 100 * c['yes'] / n,
                100 * (c['yes'] + c['plausible']) / n))
if MODE == 'sample':
    sample()
else:
    count(sys.argv[3])
