"""replay_change.py WORK BIN N [NAME...] : run `change --experimental` over the last N non-merge commits of each repository, each against its parent, and keep what the three change-time rules report.

WORK is the directory setup.sh made, and both WORK/repos and WORK/jsx-repos are read. BIN is a slop-strata binary. It writes WORK/replay-change/NAME.jsonl, one line per commit (sha, error, hits), skips a repository whose file is there, and prints the counts per repository and per rule. `labels/change-rules.tsv` joins a finding on repository, the first ten characters of the commit, rule, and path:line."""
import json, os, subprocess, sys, tempfile
WORK, BIN, N = sys.argv[1], sys.argv[2], int(sys.argv[3])
ONLY = set(sys.argv[4:])
OUT = WORK + '/replay-change'
RULES = ('x/new-case-set', 'x/new-jsx-shape', 'x/new-duplicate')
def repos():
    for sub in ('repos', 'jsx-repos'):
        d = '%s/%s' % (WORK, sub)
        for name in sorted(os.listdir(d)) if os.path.isdir(d) else []:
            if not ONLY or name in ONLY:
                yield name, '%s/%s' % (d, name)
def git(repo, *args):
    return subprocess.run(['git', '-C', repo] + list(args), capture_output=True, text=True)
def replay(name, repo, out):
    revs = git(repo, 'rev-list', '--no-merges', '-n', str(N + 1), 'HEAD').stdout.split()
    # a shallow clone has commits whose parent is not there: leave them out
    revs = [sha for sha in revs if git(repo, 'cat-file', '-e', sha + '~1^{commit}').returncode == 0][:N]
    with tempfile.NamedTemporaryFile('w', suffix='.pairs', delete=False) as f:
        f.write(''.join('%s~1 %s\n' % (sha, sha) for sha in revs))
    env = dict(os.environ, XDG_CACHE_HOME=WORK + '/cache')
    p = subprocess.run([BIN, 'change', '--repo', repo, '--pairs', f.name, '--json', '--experimental'], capture_output=True, text=True, env=env)
    os.unlink(f.name)
    with open(out + '.tmp', 'w') as w:
        for sha, line in zip(revs, p.stdout.splitlines()):
            try:
                env_ = json.loads(line)
            except ValueError:
                w.write(json.dumps({'sha': sha, 'error': 'unparsable', 'hits': []}) + '\n')
                continue
            found = (env_.get('result') or {}).get('findings') or []
            hits = [f for f in found if f.get('rule') in RULES and f.get('new', True)]
            w.write(json.dumps({'sha': sha, 'error': env_.get('error'), 'hits': hits}) + '\n')
    os.rename(out + '.tmp', out)
def main():
    os.makedirs(OUT, exist_ok=True)
    commits, total = 0, dict.fromkeys(RULES, 0)
    for name, repo in repos():
        out = '%s/%s.jsonl' % (OUT, name)
        if not os.path.exists(out):
            replay(name, repo, out)
        rows = [r for r in map(json.loads, open(out)) if not r['error']]
        count = {r: sum(1 for x in rows for h in x['hits'] if h['rule'] == r) for r in RULES}
        commits += len(rows)
        for r in RULES:
            total[r] += count[r]
        print(name, len(rows), 'commits', ' '.join('%s=%d' % (r, count[r]) for r in RULES), flush=True)
    print('all', commits, 'commits', ' '.join('%s=%d' % (r, total[r]) for r in RULES))
if __name__ == '__main__':
    main()
