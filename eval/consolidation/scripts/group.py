"""Merge per-ident candidates into per-commit groups (union-find on shared site units); write data/groups.jsonl"""
import json, glob, os, collections
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
out = open(f'{E}/data/groups.jsonl', 'w')
n = 0
for f in sorted(glob.glob(f'{E}/data/*.cands.jsonl')):
    by = collections.defaultdict(list)
    for l in open(f):
        c = json.loads(l); by[c['sha']].append(c)
    for sha, cs in by.items():
        par = list(range(len(cs)))
        def find(i):
            while par[i] != i: par[i] = par[par[i]]; i = par[i]
            return i
        owner = {}
        for i, c in enumerate(cs):
            for s in c['sites']:
                key = (s['path'], s['qual'])
                if key in owner: par[find(i)] = find(owner[key])
                else: owner[key] = i
        gs = collections.defaultdict(list)
        for i, c in enumerate(cs): gs[find(i)].append(c)
        gs_done = []
        for g in gs.values():
            sites = {}
            for c in g:
                for s in c['sites']: sites[(s['path'], s['qual'])] = s
            kinds = sorted({c['kind'] for c in g})
            xs = [c['x'] for c in g if c['x']]
            best = max(g, key=lambda c: (c['x'] is not None, c['pair_max_shape'], c['n_sites']))
            n += 1; gs_done.append(1)
            out.write(json.dumps(dict(gid=f"{best['repo']}:{sha[:10]}:{len(gs_done)-1}", repo=best['repo'], sha=sha, parent=best['parent'], ts=best['ts'], subj=best['subj'],
                      nfiles=best['nfiles'], kinds=kinds, idents=sorted({c['ident'] for c in g}), x=xs[0] if xs else None, resolved=bool(xs),
                      sites=list(sites.values()), n_sites=len(sites), pair_max_shape=max(c['pair_max_shape'] for c in g),
                      pair_max_raw=max(c['pair_max_raw'] for c in g), pair_max_jacc5=max(c['pair_max_jacc5'] for c in g), x_sim=best['x_sim_to_sites'],
                      same_file=len({p for p, q in sites}) == 1, ncands=len(g))) + '\n')
print(n)
