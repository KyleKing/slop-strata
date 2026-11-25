"""Merge groups + hand verdicts -> events.jsonl / rejected.jsonl (no history yet)."""
import json, os, statistics, subprocess
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
G = {}
for l in open(f'{E}/data/groups.jsonl'):
    g = json.loads(l); G[g['gid']] = g
LANG = {'flask': 'py', 'httpx': 'py', 'requests': 'py', 'click': 'py', 'werkzeug': 'py', 'pydantic': 'py', 'rich': 'py', 'starlette': 'py',
        'zod': 'ts', 'trpc': 'ts', 'query': 'ts', 'date-fns': 'ts', 'vite': 'ts', 'swr': 'ts', 'zustand': 'ts',
        'cobra': 'go', 'chi': 'go', 'fzf': 'go', 'bubbletea': 'go', 'gorm': 'go', 'echo': 'go', 'gin': 'go'}
def is_test(p):
    p = p.lower(); b = p.split('/')[-1]
    return 'test' in b or '/tests/' in '/' + p or '/test/' in '/' + p or '__tests__' in p or '/testsuite/' in p or '/_examples/' in p and False
def pkg(p):
    parts = p.split('/')
    return '/'.join(parts[:-1][:3])
def stm(g):
    return [s['nstmt'] for s in g['sites']]
ev, rej = [], []
seen = set()
for l in open(f'{E}/data/verdicts.tsv'):
    f = l.rstrip('\n').split('\t')
    gid, v, kind, note = f[0], f[1], f[2], f[3]
    extra = dict(x.split('=', 1) for x in f[4:] if '=' in x)
    if gid in seen: continue
    seen.add(gid)
    g = dict(G[gid])
    sites = g['sites']
    if 'drop' in extra:
        sites = [s for s in sites if not s['path'].startswith(extra['drop'])]
    if v == 'T' and len(sites) < 2:
        v = 'R'; kind = 'mirror'; note = 'after dropping deno/ mirror sites fewer than 2 remain (' + note + ')'
    if v != 'T':
        rej.append(dict(gid=gid, repo=g['repo'], sha=g['sha'], parent=g['parent'], verdict=v, reason=kind, note=note, detector_kinds=g['kinds'],
                        stratum=('S3_pullup' if 'pullup' in g['kinds'] else 'S1_extract_res' if g['kinds'] == ['extract'] and g['resolved'] else 'S2_extract_unres' if g['kinds'] == ['extract'] else 'S4_merge_delete'),
                        n_sites=g['n_sites'], subj=g['subj'][:100]))
        continue
    paths = [s['path'] for s in sites]
    x = g['x']
    ntok = [s['ntok'] for s in sites]; nst = [s['nstmt'] for s in sites]
    e = dict(gid=gid, repo=g['repo'], lang=LANG[g['repo']], sha=g['sha'], parent=g['parent'], commit_ts=g['ts'], subj=g['subj'][:120],
             nfiles_in_commit=g['nfiles'], kind=kind, note=note, detector_kinds=g['kinds'],
             stratum=('S3_pullup' if 'pullup' in g['kinds'] else 'S1_extract_res' if g['kinds'] == ['extract'] and g['resolved'] else 'S2_extract_unres' if g['kinds'] == ['extract'] else 'S4_merge_delete'),
             sites=[dict(path=s['path'], qual=s['qual'], unit_lines=s['unit_lines'], region_lines=s['region_lines'], ntok=s['ntok'], nstmt=s['nstmt']) for s in sites],
             target=dict(name=extra.get('x') or (x and x['qual']), path=x and x['path'], lines=x and x['lines'], kind=x and x['kind'], from_detector=bool(x) and 'x' not in extra),
             n_sites=len(sites), ntok_min=min(ntok), ntok_median=statistics.median(ntok), ntok_max=max(ntok),
             nstmt_min=min(nst), nstmt_median=statistics.median(nst),
             same_file=len(set(paths)) == 1, same_dir=len({os.path.dirname(p) for p in paths}) == 1, cross_package=len({pkg(p) for p in paths}) > 1,
             test_only=all(is_test(p) for p in paths), any_test=any(is_test(p) for p in paths),
             sim_pair_max_raw=g['pair_max_raw'], sim_pair_max_shape=g['pair_max_shape'], sim_pair_max_jacc5=g['pair_max_jacc5'],
             hand_verified_from='region-level view of parent regions + target head (+ full diff where noted in note); see SCHEMA.md', source='structural')
    if 'partially read' in note or 'partial' in note: e['partial_diff'] = True
    if len(sites) < 2: continue
    ev.append(e)
with open(f'{E}/events_pre.jsonl', 'w') as f:
    for e in ev: f.write(json.dumps(e) + '\n')
with open(f'{E}/rejected.jsonl', 'w') as f:
    for e in rej: f.write(json.dumps(e) + '\n')
print(len(ev), len(rej))
