"""Compact view of groups: view2.py gid [gid...]   (full=1 env for diff)"""
import sys, json, subprocess, os
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
G = {}
for l in open(f'{E}/data/groups.jsonl'):
    g = json.loads(l); G[g['gid']] = g
def show(path, rev, a, b, n=5, w=110):
    out = subprocess.run(['git', '-C', f'{E}/repos/{g["repo"]}', 'show', f'{rev}:{path}'], capture_output=True, text=True, errors='replace').stdout.split('\n')
    seg = out[a-1:b]
    return [l.rstrip()[:w] for l in seg if l.strip()][:n]
for gid in sys.argv[1:]:
    g = G[gid]
    print(f"### {gid} {g['subj'][:80]} files={g['nfiles']} kinds={g['kinds']} ids={g['idents'][:4]} sites={g['n_sites']} shape={g['pair_max_shape']} jacc={g['pair_max_jacc5']}")
    for s in g['sites'][:3]:
        print(f"  S {s['path']}:{s['qual']} L{s['region_lines']} tok={s['ntok']} -> new{s['ntok_new']}")
        for l in show(s['path'], g['parent'], s['region_lines'][0], s['region_lines'][1], 2, 100): print('     -', l)
    if len(g['sites']) > 3: print(f"  ... +{len(g['sites'])-3} more sites: " + ', '.join(s['qual'] for s in g['sites'][3:8]))
    if g['x']:
        x = g['x']; print(f"  X {x['path']}:{x['qual']} {x['kind']} L{x['lines']}")
        for l in show(x['path'], g['sha'], x['lines'][0], x['lines'][1], 4): print('     +', l)
