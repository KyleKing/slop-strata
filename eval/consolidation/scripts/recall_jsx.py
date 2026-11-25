"""recall_jsx.py WORK BIN find NAME... | eval TSV : mine commits that extracted a shared component, and ask whether a JSX group held the sites at the parent.

WORK is the directory setup.sh made, and the repositories are read from WORK/jsx-repos. BIN is a slop-strata binary built with `jsxGroupLimit` lifted (README.md says how), so every rank is in the JSON.

`find` lists candidates as JSON lines: a commit of at most 60 files that adds a .tsx or .jsx file declaring a component, where two files that were there before each replace markup by a use of it. A site is a hunk of the parent that loses at least two more tags than it gains and gains `<Component`. A person reads the candidates and keeps the extractions.

`eval` reads the kept ones from TSV (repository, commit, component in its first three columns), snapshots each commit's parent, and prints repository, commit, component, sites, hit, and rank. A group covers a site when one of its sites is in the hunk's file and shares a line with it, and an extraction is a hit when one group covers two sites."""
import json, os, re, subprocess, sys
WORK, BIN, MODE = sys.argv[1], sys.argv[2], sys.argv[3]
EXT = ('.tsx', '.jsx')
SKIP = re.compile(r'(^|/)(__tests__|tests?|e2e|stories|storybook|examples?|playground)/|\.(test|spec|stories)\.')
DECL = re.compile(r'^\+\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function|const|class)\s+([A-Z][A-Za-z0-9_]*)', re.M)
TAG = re.compile(r'<[A-Za-z][A-Za-z0-9_.]*')
HUNK = re.compile(r'^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@.*$', re.M)
def git(repo, *args):
    return subprocess.run(['git', '-C', repo] + list(args), capture_output=True, text=True, errors='replace').stdout
def sites(repo, sha, name, files=None):
    """The hunks of sha's parent that replaced markup by <name: (file, first line, last line, tags lost)."""
    if files is None:
        status = [l.split('\t') for l in git(repo, 'diff', '--no-renames', '--name-status', sha + '~1', sha).splitlines()]
        files = [f for s, f in status if s == 'M' and f.endswith(EXT)]
    use, out = re.compile('<' + re.escape(name) + r'\b'), []
    for f in files:
        parts = HUNK.split(git(repo, 'diff', '-U0', sha + '~1', sha, '--', f))
        for i in range(1, len(parts), 5):
            start, count, body = int(parts[i]), parts[i + 1], parts[i + 4]
            count = 1 if count is None else int(count)
            plus = '\n'.join(l[1:] for l in body.splitlines() if l.startswith('+'))
            minus = '\n'.join(l[1:] for l in body.splitlines() if l.startswith('-'))
            if count and use.search(plus) and not use.search(minus):
                lost = len(TAG.findall(minus)) - (len(TAG.findall(plus)) - len(use.findall(plus)))
                if lost >= 2:
                    out.append((f, start, start + count - 1, lost))
    return out
def find():
    for name in sys.argv[4:]:
        repo = '%s/jsx-repos/%s' % (WORK, name)
        log = git(repo, 'log', '--no-merges', '--no-renames', '--name-status', '--format=%x01%H %P')
        for block in log.split('\x01')[1:]:
            head, _, body = block.partition('\n')
            if len(head.split()) < 2:
                continue  # no parent in a shallow clone
            sha = head.split()[0]
            files = [l.split('\t') for l in body.strip().splitlines() if '\t' in l]
            added = [f for s, f in files if s == 'A' and f.endswith(EXT) and not SKIP.search(f)]
            changed = [f for s, f in files if s == 'M' and f.endswith(EXT) and not SKIP.search(f)]
            if len(files) > 60 or not added or len(changed) < 2:
                continue
            names = {}
            for f in added:
                for m in DECL.finditer(git(repo, 'show', '--format=', sha, '--', f)):
                    names.setdefault(m.group(1), f)
            for comp, new in names.items():
                found = sites(repo, sha, comp, changed)
                per = {}
                for f, _, _, lost in found:
                    per[f] = per.get(f, 0) + lost
                if sum(1 for lost in per.values() if lost >= 3) >= 2:
                    print(json.dumps({'repo': name, 'sha': sha, 'component': comp, 'new_file': new, 'sites': found}), flush=True)
def rank_of(groups, found):
    for rank, g in enumerate(groups, 1):
        covered = {i for i, (f, lo, hi, _) in enumerate(found) for s in g['sites'] if s['path'] == f and s['line'] <= hi and lo <= s['end_line']}
        if len(covered) >= 2:
            return rank
    return None
def evaluate():
    out = WORK + '/recall-jsx'
    os.makedirs(out, exist_ok=True)
    env = dict(os.environ, XDG_CACHE_HOME=WORK + '/cache')
    rows = []
    for line in open(sys.argv[4]):
        name, sha, comp = line.rstrip('\n').split('\t')[:3]
        repo = '%s/jsx-repos/%s' % (WORK, name)
        snap = '%s/%s-%s.json' % (out, name, sha)
        if not os.path.exists(snap):
            p = subprocess.run([BIN, 'snapshot', '--repo', repo, '--rev', sha + '~1', '--experimental', '--json'], capture_output=True, text=True, env=env)
            json.dump((json.loads(p.stdout).get('result') or {}).get('jsx_groups'), open(snap, 'w'))
        groups = (json.load(open(snap)) or {}).get('groups') or []
        found = sites(repo, sha, comp)
        rank = rank_of(groups, found)
        rows.append(rank)
        print(name, sha, comp, len(found), 'yes' if rank else 'no', rank or '-', sep='\t', flush=True)
    hits = [r for r in rows if r]
    print('extractions %d, hit %d, in the first 20 %d' % (len(rows), len(hits), sum(1 for r in hits if r <= 20)))
if __name__ == '__main__':
    find() if MODE == 'find' else evaluate()
