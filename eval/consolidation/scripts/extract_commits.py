"""extract_commits.py WORK BIN find|check [REPO:SHA10...] : find commits whose subject and diff look like an extract-method, then ask whether the function was a split candidate at the parent.

WORK is the directory setup.sh made. BIN is a slop-strata binary built with the list limits lifted (README.md says how)."""
import json, os, re, subprocess, sys
WORK, BIN, MODE = sys.argv[1], sys.argv[2], sys.argv[3]
REPOS = WORK + '/repos'
OUT = WORK + '/extract'
EXT = ('.go', '.py', '.ts', '.tsx')
def git(repo, *args):
    return subprocess.run(['git', '-C', repo] + list(args), capture_output=True, text=True, errors='replace').stdout
def hunks(repo, sha):
    """Per source file: (old_start, old_count, new_start, new_count) for each hunk of parent..sha."""
    out = {}
    path = None
    for line in git(repo, 'diff', '-U0', '--no-renames', sha + '~1', sha).split('\n'):
        if line.startswith('+++ '):
            path = line[6:] if line.startswith('+++ b/') else None
        elif line.startswith('@@') and path and path.endswith(EXT) and 'test' not in path.lower():
            m = re.match(r'@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@', line)
            a, ac, b, bc = int(m.group(1)), int(m.group(2) or 1), int(m.group(3)), int(m.group(4) or 1)
            out.setdefault(path, []).append((a, ac, b, bc))
    return out
def find():
    os.makedirs(OUT, exist_ok=True)
    rows = []
    for repo in sorted(os.listdir(REPOS)):
        r = REPOS + '/' + repo
        log = git(r, 'log', '--no-merges', '--format=%H\t%s', '-i', '-E', '--grep=extract|split|factor out|break (up|down|out)|decompose|pull out|move .* (in)?to (a |its )?(own |separate |new )?(function|method|helper)')
        for line in log.split('\n'):
            if '\t' not in line:
                continue
            sha, subj = line.split('\t', 1)
            hs = hunks(r, sha)
            if not hs or len(hs) > 3:
                continue
            for path, hl in hs.items():
                shrink = [h for h in hl if h[1] >= 8 and h[3] <= h[1] // 2]
                grow = [h for h in hl if h[3] >= 6 and h[1] <= 1]
                if shrink and grow:
                    rows.append({'repo': repo, 'sha': sha, 'subj': subj, 'path': path, 'shrink': shrink, 'grow': grow})
    json.dump(rows, open(OUT + '/candidates.json', 'w'), indent=1)
    print(len(rows), 'candidates')
    for x in rows:
        print(x['repo'], x['sha'][:10], x['path'], x['shrink'][:2], '|', x['subj'][:70])
def check():
    rows = json.load(open(OUT + '/candidates.json'))
    keep = set(sys.argv[4:])
    env = dict(os.environ, XDG_CACHE_HOME=WORK + '/cache')
    for x in rows:
        key = x['repo'] + ':' + x['sha'][:10]
        if keep and key not in keep:
            continue
        out = '%s/%s_%s.json' % (OUT, x['repo'], x['sha'][:10])
        if not os.path.exists(out):
            p = subprocess.run([BIN, 'snapshot', '--repo', REPOS + '/' + x['repo'], '--rev', x['sha'] + '~1', '--json', '--experimental'], capture_output=True, env=env)
            json.dump(json.loads(p.stdout)['result'].get('split_candidates'), open(out, 'w'))
        sc = json.load(open(out))
        res = []
        for a, ac, _, _ in x['shrink']:
            lo, hi = a, a + ac - 1
            for rank, f in enumerate(sc['functions']):
                if f['file'] == x['path'] and f['line'] <= lo and hi <= f['end_line']:
                    sp = f['split']
                    ov = max(0, min(hi, sp['end_line']) - max(lo, sp['start_line']) + 1)
                    res.append('removed %d-%d in %s (cog %d) rank %d/%d block %d-%d overlap %d/%d lines' % (lo, hi, f['name'], f['cognitive'], rank + 1, sc['total'], sp['start_line'], sp['end_line'], ov, ac))
                    break
            else:
                res.append('removed %d-%d: no listed function holds it' % (lo, hi))
        print(key, x['path'], '|', x['subj'][:60])
        for r in res:
            print('    ', r)
if __name__ == '__main__':
    find() if MODE == 'find' else check()
