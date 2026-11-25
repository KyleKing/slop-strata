"""replay_groups.py WORK BIN run|eval [FLAGS] : snapshot each mined event's parent with BIN, then say whether a duplicate group holds two of its sites.

WORK is the directory setup.sh made. BIN is a slop-strata binary built with the list limits lifted (README.md says how), so every rank is in the JSON."""
import json, os, subprocess, sys, statistics
WORK, BIN, MODE = sys.argv[1], sys.argv[2], sys.argv[3]
OUT = WORK + '/replay'
events = [json.loads(l) for l in open(WORK + '/events.jsonl')]
def name(e):
    return e['gid'].replace(':', '_').replace('/', '_')
def run():
    os.makedirs(OUT, exist_ok=True)
    env = dict(os.environ, XDG_CACHE_HOME=WORK + '/cache')
    for e in events:
        out = '%s/%s.json' % (OUT, name(e))
        if os.path.exists(out):
            continue
        repo = '%s/repos/%s' % (WORK, e['repo'])
        flags = sys.argv[4:]
        p = subprocess.run([BIN, 'snapshot', '--repo', repo, '--rev', e['parent'], '--json'] + flags, capture_output=True, env=env)
        try:
            res = json.loads(p.stdout)['result']
            keep = {'duplicate_groups': res.get('duplicate_groups'), 'split_candidates': res.get('split_candidates')}
        except Exception as err:
            keep = {'error': str(err) + p.stderr.decode()[:300]}
        json.dump(keep, open(out, 'w'))
        print(e['gid'], 'ok' if 'error' not in keep else keep['error'][:100], flush=True)
def overlaps(gs, es):
    lo, hi = es['region_lines']
    return gs['path'] == es['path'] and gs['line'] <= hi and lo <= gs['last_line'] + 1
def rank_of(e, groups):
    for rank, g in enumerate(groups):
        hit = {i for i, es in enumerate(e['sites']) for gs in g['sites'] if overlaps(gs, es)}
        if len(hit) >= 2:
            return rank + 1
    return None
def evaluate():
    rows = []
    for e in events:
        d = json.load(open('%s/%s.json' % (OUT, name(e))))
        if 'error' in d or not d.get('duplicate_groups'):
            rows.append((e, None, 'error')); continue
        rows.append((e, rank_of(e, d['duplicate_groups']['groups']), d['duplicate_groups']['total']))
    def line(label, sel):
        sub = [r for r in rows if sel(r[0])]
        found = [r[1] for r in sub if r[1]]
        med = statistics.median(found) if found else 0
        top20 = sum(1 for x in found if x <= 20)
        print('%-28s n=%3d found=%3d (%2.0f%%) in top 20=%3d median rank=%s' % (label, len(sub), len(found), 100 * len(found) / max(1, len(sub)), top20, med))
    line('all', lambda e: True)
    line('production sites only', lambda e: not e['any_test'])
    line('test only', lambda e: e['test_only'])
    for lang in ('py', 'ts', 'go'):
        line('lang ' + lang + ' (prod)', lambda e: e['lang'] == lang and not e['any_test'])
    for lo, hi, lab in ((0, 15, '<15 tok'), (15, 25, '15-24'), (25, 50, '25-49'), (50, 10**9, '>=50')):
        line('median site ' + lab + ' (prod)', lambda e: lo <= e['ntok_median'] < hi and not e['any_test'])
    print('errors', sum(1 for r in rows if r[2] == 'error'))
    if len(sys.argv) > 4:
        for e, rank, total in rows:
            print(e['gid'], e['ntok_median'], e['nstmt_median'], 'test' if e['any_test'] else 'prod', rank, total)
if __name__ == '__main__':
    run() if MODE == 'run' else evaluate()
