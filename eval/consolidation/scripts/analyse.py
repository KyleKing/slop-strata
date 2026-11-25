import json, os, collections, random, statistics as st, glob
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ev = [json.loads(l) for l in open(f'{E}/events.jsonl')]
ct = [json.loads(l) for l in open(f'{E}/controls.jsonl')]
ver = [l.rstrip('\n').split('\t') for l in open(f'{E}/data/verdicts.tsv')]
out = []
def P(*a):
    s = ' '.join(str(x) for x in a); print(s); out.append(s)
def pct(n, d): return f"{n}/{d} ({100*n/d:.0f}%)" if d else "0/0"
P('== EVENTS', len(ev), 'repos', len({e['repo'] for e in ev}), 'distinct commits', len({(e['repo'], e['sha']) for e in ev}))
P('per repo', dict(sorted(collections.Counter(e['repo'] for e in ev).items(), key=lambda x: -x[1])))
P('per lang', collections.Counter(e['lang'] for e in ev))
P('kind', collections.Counter(e['kind'] for e in ev))
P('stratum', collections.Counter(e['stratum'] for e in ev))
# type-level (alias/TypeVar) events
def typelevel(e):
    t = e['note'].lower()
    return any(k in t for k in ['typevar', 'alias', 'type replaces', 'annotation'])
P('type-level (alias/TypeVar/annotation) events', pct(sum(typelevel(e) for e in ev), len(ev)))
P('-- size')
P('ntok_min <25', pct(sum(e['ntok_min'] < 25 for e in ev), len(ev)), '| ntok_median <25', pct(sum(e['ntok_median'] < 25 for e in ev), len(ev)), '| ntok_median<15', pct(sum(e['ntok_median'] < 15 for e in ev), len(ev)))
P('nstmt_min <6', pct(sum(e['nstmt_min'] < 6 for e in ev), len(ev)), '| nstmt_median<6', pct(sum(e['nstmt_median'] < 6 for e in ev), len(ev)), '| nstmt_min==1', pct(sum(e['nstmt_min'] == 1 for e in ev), len(ev)))
qs = lambda v: [round(x, 1) for x in st.quantiles(v, n=4)]
P('ntok_median quartiles', qs([e['ntok_median'] for e in ev]), 'max', max(e['ntok_median'] for e in ev))
P('n_sites dist', sorted(collections.Counter(min(e['n_sites'], 8) for e in ev).items()), '(8 = 8+)')
P('same_file', pct(sum(e['same_file'] for e in ev), len(ev)), 'same_dir (incl same file)', pct(sum(e['same_dir'] for e in ev), len(ev)), 'cross_package', pct(sum(e['cross_package'] for e in ev), len(ev)))
P('test_only', pct(sum(e['test_only'] for e in ev), len(ev)), 'any_test', pct(sum(e['any_test'] for e in ev), len(ev)))
P('-- similarity of sites before unification (best pair)')
j5 = [e['sim_pair_max_jacc5'] for e in ev]; sh = [e['sim_pair_max_shape'] for e in ev]; rw = [e['sim_pair_max_raw'] for e in ev]
P('jacc5>=0.5', pct(sum(x >= .5 for x in j5), len(ev)), '| >=0.3', pct(sum(x >= .3 for x in j5), len(ev)), '| <0.3 but shape>=0.7', pct(sum(a < .3 and b >= .7 for a, b in zip(j5, sh)), len(ev)), '| jacc5==0', pct(sum(x == 0 for x in j5), len(ev)))
P('old detector (jacc5>=0.5 & ntok_min>=25)', pct(sum(a >= .5 and e['ntok_min'] >= 25 for a, e in zip(j5, ev)), len(ev)))
P('old detector, any-pair (jacc5>=0.5 & ntok_median>=25)', pct(sum(a >= .5 and e['ntok_median'] >= 25 for a, e in zip(j5, ev)), len(ev)))
P('raw seq-ratio >=0.5', pct(sum(x >= .5 for x in rw), len(ev)), 'shape seq-ratio quartiles', qs(sh), 'raw quartiles', qs(rw))
P('holes (best pair) quartiles', qs([e['holes_best_pair'] for e in ev]), 'zero holes', pct(sum(e['holes_best_pair'] == 0 for e in ev), len(ev)), 'unaligned==0', pct(sum(e['unaligned_best_pair'] == 0 for e in ev), len(ev)))
# detector families (mutually non-exclusive)
fam = collections.Counter()
for e in ev:
    a = e['sim_pair_max_jacc5']
    f = []
    if a >= .5 and e['ntok_median'] >= 25: f.append('F1_token_clone>=25tok')
    if a >= .5 and e['ntok_median'] < 25: f.append('F2_small_fragment_jacc>=.5')
    if a < .5 and e['sim_pair_max_shape'] >= .7: f.append('F3_same_shape_diff_tokens')
    if a < .5 and e['sim_pair_max_shape'] < .7: f.append('F3b_weak_shape')
    if e['sibling_class_methods'] or e['kind'] == 'pullup': f.append('F4_sibling_class_methods')
    if typelevel(e): f.append('F5_type_level')
    for x in f: fam[x] += 1
P('families (non-exclusive)', dict(fam))
excl = collections.Counter()
for e in ev:
    a = e['sim_pair_max_jacc5']
    excl['F1' if a >= .5 and e['ntok_median'] >= 25 else 'F2' if a >= .5 else 'F3' if e['sim_pair_max_shape'] >= .7 else 'F3b'] += 1
P('exclusive by similarity: ', dict(excl))
P('-- history (events with >=2 aged sites)')
H = [e for e in ev if e['hist'].get('n_aged', 0) >= 2]
P('n', len(H))
lt = [e['hist']['dup_lifetime_days'] for e in H]; sp = [e['hist']['birth_spread_days'] for e in H]
P('dup lifetime days (since last site born) quartiles', qs(lt), 'median', st.median(lt), '| <30d', pct(sum(x < 30 for x in lt), len(H)), '<1y', pct(sum(x < 365 for x in lt), len(H)), '>=2y', pct(sum(x >= 730 for x in lt), len(H)))
P('oldest site age days quartiles', qs([e['hist']['oldest_site_age_days'] for e in H]))
P('same birth commit', pct(sum(e['hist']['same_birth_commit'] for e in H), len(H)), '| later site born after earlier by a different commit', pct(sum(e['hist']['later_site_after_earlier'] for e in H), len(H)))
P('  of those: spread>=30d', pct(sum(e['hist']['later_site_after_earlier'] and e['hist']['birth_spread_days'] >= 30 for e in H), len(H)), 'spread>=365d', pct(sum(e['hist']['later_site_after_earlier'] and e['hist']['birth_spread_days'] >= 365 for e in H), len(H)))
LS = [e for e in H if e['hist']['later_site_after_earlier']]
P('same author as earlier site (among later-after-earlier with author info)', pct(sum(1 for e in LS if e['hist']['same_author_as_earlier']), sum(1 for e in LS if e['hist']['same_author_as_earlier'] is not None)))
P('diverged (site-specific later edits visible in blame)', pct(sum(e['hist']['diverged'] for e in H), len(H)), '| lockstep commits>=1', pct(sum(e['hist']['lockstep_commits'] >= 1 for e in H), len(H)))
P('prior co-change commits quartiles', qs([e['hist']['cochange_commits'] for e in H]))
# ---- controls
P('\n== CONTROLS', len(ct), 'events with controls', len({c['event_gid'] for c in ct}), 'repos', len({c['repo'] for c in ct}), 'tight', sum(c['match_quality'] == 'tight' for c in ct))
cby = collections.defaultdict(list)
for c in ct: cby[c['event_gid']].append(c)
EV = [e for e in ev if e['gid'] in cby]
P('events used in paired comparison', len(EV), 'repos', len({e['repo'] for e in EV}), 'per repo', dict(collections.Counter(e['repo'] for e in EV)))
def ev_val(e, v):
    h = e['hist']
    return {'sim_shape': e['sim_pair_max_shape'], 'sim_raw': e['sim_pair_max_raw'], 'sim_jacc5': e['sim_pair_max_jacc5'], 'holes': e['holes_best_pair'],
            'ntok_median': e['ntok_median'], 'nstmt_min': e['nstmt_min'], 'same_file': float(e['same_file']), 'same_dir': float(e['same_dir']), 'cross_package': float(e['cross_package']),
            'cochange_commits': h.get('cochange_commits'), 'prior_commits_per_file': h.get('prior_commits_per_file'), 'prior_12m_per_file': h.get('prior_commits_12m_per_file'),
            'birth_spread_days': h.get('birth_spread_days'), 'dup_lifetime_days': h.get('dup_lifetime_days'), 'diverged': None if 'diverged' not in h else float(h['diverged']),
            'later_after_earlier': None if 'later_site_after_earlier' not in h else float(h['later_site_after_earlier'])}[v]
def ct_val(c, v):
    h = c['hist']
    return {'sim_shape': c['sim_pair_max_shape'], 'sim_raw': c['sim_pair_max_raw'], 'sim_jacc5': c['sim_pair_max_jacc5'], 'holes': c['holes'],
            'ntok_median': c['ntok_median'], 'nstmt_min': c['nstmt_min'], 'same_file': float(c['same_file']), 'same_dir': float(c['same_dir']), 'cross_package': float(c['cross_package']),
            'cochange_commits': h.get('cochange_commits'), 'prior_commits_per_file': h.get('prior_commits_per_file'), 'prior_12m_per_file': h.get('prior_commits_12m_per_file'),
            'birth_spread_days': h.get('birth_spread_days'), 'dup_lifetime_days': h.get('dup_lifetime_days'), 'diverged': None if 'diverged' not in h else float(h['diverged']),
            'later_after_earlier': None if 'later_site_after_earlier' not in h else float(h['later_site_after_earlier'])}[v]
random.seed(1)
def paired(v):
    rows = []
    for e in EV:
        a = ev_val(e, v); bs = [ct_val(c, v) for c in cby[e['gid']]]; bs = [b for b in bs if b is not None]
        if a is None or not bs: continue
        rows.append((e['repo'], a, sum(bs) / len(bs)))
    return rows
def boot(rows, B=2000):
    repos = sorted({r[0] for r in rows}); by = collections.defaultdict(list)
    for r in rows: by[r[0]].append(r)
    ds = []
    for _ in range(B):
        samp = [random.choice(repos) for _ in repos]
        d = [x[1] - x[2] for rp in samp for x in by[rp]]
        if d: ds.append(sum(d) / len(d))
    ds.sort(); return ds[int(.025 * len(ds))], ds[int(.975 * len(ds))]
P('variable | n | event mean | control mean | diff | 95% repo-cluster bootstrap CI | repos where diff>0 / <0')
for v in ['sim_shape', 'sim_raw', 'sim_jacc5', 'holes', 'ntok_median', 'nstmt_min', 'same_file', 'same_dir', 'cross_package', 'cochange_commits', 'prior_commits_per_file', 'prior_12m_per_file', 'birth_spread_days', 'dup_lifetime_days', 'diverged', 'later_after_earlier']:
    r = paired(v)
    if len(r) < 10: P(v, 'n<10'); continue
    em = sum(x[1] for x in r) / len(r); cm = sum(x[2] for x in r) / len(r)
    lo, hi = boot(r)
    byr = collections.defaultdict(float)
    for x in r: byr[x[0]] += x[1] - x[2]
    P(f"{v:24s} n={len(r):3d} ev={em:9.3f} ct={cm:9.3f} diff={em-cm:9.3f} CI=[{lo:8.3f},{hi:8.3f}] repos +{sum(1 for x in byr.values() if x>0)}/-{sum(1 for x in byr.values() if x<0)}")
# control family shares
P('controls: jacc5>=0.5', pct(sum(c['sim_pair_max_jacc5'] >= .5 for c in ct), len(ct)), 'ntok<25', pct(sum(c['ntok_median'] < 25 for c in ct), len(ct)), 'same_file', pct(sum(c['same_file'] for c in ct), len(ct)))
# message
P('\n== message flags among verified events: any flagged word', pct(sum(bool(e['message_flags']) for e in ev), len(ev)), '| strong words (dedup/duplicat/dry/consolidat/factor/unify/reuse/share)', pct(sum(any(w in ('dedup','de-dup','duplicat','dry','consolidat','factor out','unify','reuse','re-use','share','shared','extract common','pull up') for w in e['message_flags']) for e in ev), len(ev)))
P('events by commit-level: events/commit', st.mean(collections.Counter((e['repo'], e['sha']) for e in ev).values()))
# detector precision by stratum from reviewed sample
res = collections.defaultdict(collections.Counter)
G = {}
for l in open(f'{E}/data/groups.jsonl'):
    g = json.loads(l); G[g['gid']] = g
def stratum(g):
    k = g['kinds']
    return 'S3_pullup' if 'pullup' in k else ('S1_extract_resolved' if g['resolved'] else 'S2_extract_unresolved') if k == ['extract'] else 'S4_merge_delete'
tot = collections.Counter(stratum(g) for g in G.values())
for f in ver:
    res[stratum(G[f[0]])][f[1]] += 1
P('\n== detector precision by stratum (reviewed random sample): groups total | reviewed | T | U | R | precision T/(T+R+U) | est. true groups in stratum')
for s in sorted(tot):
    r = res[s]; n = sum(r.values())
    P(f"{s:24s} total={tot[s]:4d} reviewed={n:3d} T={r['T']:3d} U={r['U']:2d} R={r['R']:3d} prec={r['T']/n:.2f} est_true={tot[s]*r['T']/n:.0f}")
rej = collections.Counter()
for f in ver:
    if f[1] != 'T': rej[f[2]] += 1
P('reject reasons', dict(rej.most_common()))
# per repo funnel
cnt = collections.defaultdict(lambda: collections.Counter())
for g in G.values(): cnt[g['repo']]['groups'] += 1
for f in ver: cnt[G[f[0]]['repo']]['reviewed'] += 1
for e in ev: cnt[e['repo']]['events'] += 1
cm = collections.defaultdict(int)
for f in glob.glob(f'{E}/data/*.commits.jsonl'):
    r = os.path.basename(f).split('.')[0]
    for l in open(f):
        c = json.loads(l); cm[r] += 1
P('\n== per-repo funnel: commits(non-merge) | groups(candidates) | reviewed | verified events | controls')
cc = collections.Counter(c['repo'] for c in ct)
for r in sorted(cm): P(f"{r:10s} commits={cm[r]:5d} cand_groups={cnt[r]['groups']:4d} reviewed={cnt[r]['reviewed']:3d} events={cnt[r]['events']:3d} controls={cc[r]:3d}")
open(f'{E}/analysis.txt', 'w').write('\n'.join(out))
