"""Matched negative controls. For each event: sample code-region PAIRS at the parent commit, matched on size (token count band),
test/prod status, and token-shape similarity (+-0.15 of the event's best pair), from the same repo, then keep those still present
(both units exist and the region persists with >=80% longest-match) 24 months later and not flagged as a site of any structural
candidate in (parent, parent+24m]."""
import sys, os, json, random, subprocess, itertools, collections
sys.path.insert(0, os.path.dirname(__file__))
from difflib import SequenceMatcher
import units as U
from sim import ratio, jacc, holes
from multiprocessing import Pool
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP = ('node_modules/', 'vendor/', 'dist/', 'build/', 'third_party/', '/testdata/', 'fixtures/', 'locale/', 'locales/', '.min.', 'deno/', '/docs/')
MONTH24 = 730 * 86400
RELAX = os.environ.get('RELAX') == '1'
TOL = 0.30 if RELAX else 0.15
LOF, HIF = (0.4, 2.5) if RELAX else (0.6, 1.6)
HEAD_TS = {}
def git(repo, *a):
    return subprocess.run(['git', '-C', repo] + list(a), capture_output=True, text=True, errors='replace').stdout
def blob(repo, rev, path):
    r = subprocess.run(['git', '-C', repo, 'show', f'{rev}:{path}'], capture_output=True)
    return r.stdout if r.returncode == 0 else None
def is_test(p):
    p = p.lower(); b = p.split('/')[-1]
    return 'test' in b or '/tests/' in '/' + p or '/test/' in '/' + p or '__tests__' in p or '/testsuite/' in p
CACHE = {}
def units_of(repo, rev, path, lang):
    key = (repo, rev, path)
    if key in CACHE: return CACHE[key]
    b = blob(repo, rev, path)
    if b is None or len(b) > 200_000: CACHE[key] = []; return []
    try: us = U.extract(b, lang, path)[0]
    except Exception: us = []
    CACHE[key] = us; return us

def windows(u, lo, hi, maxw=40):
    """statement-aligned token windows inside unit u with lo<=len<=hi"""
    if u.kind != 'func' or len(u.toks) < lo: return []
    # statement runs
    runs = []; cur = None; start = 0
    for i, s in enumerate(u.stmts):
        if s is None:
            if cur is not None or start < i: runs.append((start, i)) if cur is not None else None
            cur = None; start = i + 1; continue
        if s != cur:
            if cur is not None: runs.append((start, i))
            cur = s; start = i
    if cur is not None: runs.append((start, len(u.stmts)))
    out = []
    for a in range(len(runs)):
        for b in range(a, len(runs)):
            i1, i2 = runs[a][0], runs[b][1]
            n = i2 - i1
            if n > hi: break
            if n >= lo: out.append((i1, i2))
    if len(out) > maxw: out = random.sample(out, maxw)
    return out

def event_controls(e, rng, ncontrols=5):
    repo = f"{E}/repos/{e['repo']}"
    lang = {'py': 'py', 'ts': 'ts', 'go': 'go'}[e['lang']]
    P, ts = e['parent'], e['commit_ts']
    head_ts = int(git(repo, 'log', '-1', '--format=%ct', 'HEAD').strip())
    if ts + MONTH24 > head_ts:
        return dict(censored=True, controls=[])
    later = git(repo, 'rev-list', '-1', f'--before={ts + MONTH24}', 'HEAD').strip()
    files = [f for f in git(repo, 'ls-tree', '-r', '--name-only', P).split('\n') if f and U.lang_for(f) and
             (U.REPO_LANG[U.lang_for(f)] == lang) and not any(s in f for s in SKIP) and not f.endswith('.d.ts')]
    test_only = e['test_only']
    files = [f for f in files if is_test(f) == test_only]
    site_dirs = {os.path.dirname(s['path']) for s in e['sites']}
    near = [f for f in files if os.path.dirname(f) in site_dirs]
    top = {s['path'].split('/')[0] for s in e['sites']}
    mid = [f for f in files if f.split('/')[0] in top and f not in near]
    rest = [f for f in files if f not in near and f not in mid]
    rng.shuffle(mid); rng.shuffle(rest); rng.shuffle(near)
    pool_files = near[:40] + mid[:60] + rest[:90]
    m = max(8, int(e['ntok_median']))
    lo, hi = max(6, int(LOF * m)), max(12, int(HIF * m))
    sitekeys = {(s['path'], s['qual']) for s in e['sites']}
    wins = []
    for f in pool_files:
        for u in units_of(repo, P, f, lang):
            if (u.path, u.qual) in sitekeys or u.kind != 'func': continue
            for (i1, i2) in windows(u, lo, hi, maxw=6):
                wins.append((u, i1, i2))
    if len(wins) < 20: return dict(censored=False, controls=[], note='pool too small')
    rng.shuffle(wins); wins = wins[:700]
    sh = [set(tuple(u.norm[i:i + 3]) for i in range(i1, max(i1 + 1, i2 - 2))) for (u, i1, i2) in wins]
    target = e['sim_pair_max_shape']
    # candidate pairs by cheap 3-gram jaccard
    cand = []
    for a in range(len(wins)):
        sa = sh[a]
        if not sa: continue
        for b in range(a + 1, len(wins)):
            sb = sh[b]
            if not sb: continue
            inter = len(sa & sb)
            if inter == 0 and target > 0.3: continue
            j = inter / (len(sa) + len(sb) - inter)
            cand.append((j, a, b))
    rng.shuffle(cand)
    chosen = []; used_units = set()
    # evaluate accurate shape ratio for a bounded number of candidates in order of closeness to target proxy
    cand.sort(key=lambda t: abs(t[0] - target * 0.8))
    evaluated = 0
    for j, a, b in cand:
        if len(chosen) >= ncontrols * 10 or evaluated > 4000: break
        ua, i1a, i2a = wins[a]; ub, i1b, i2b = wins[b]
        if (ua.path, ua.qual) == (ub.path, ub.qual): continue
        ka, kb = (ua.path, ua.qual), (ub.path, ub.qual)
        if ka in used_units or kb in used_units: continue
        evaluated += 1
        na, nb = ua.norm[i1a:i2a], ub.norm[i1b:i2b]
        sr = ratio(na, nb)
        if abs(sr - target) > TOL: continue
        chosen.append((a, b, sr)); used_units.add(ka); used_units.add(kb)
    out = []
    # later-snapshot + candidate-flag check
    flagged = set()
    cfile = f"{E}/data/{e['repo']}.cands.jsonl"
    for l in open(cfile):
        c = json.loads(l)
        if ts < c['ts'] <= ts + MONTH24:
            for s in c['sites']: flagged.add((s['path'], s['qual']))
    for a, b, sr in chosen:
        if len(out) >= ncontrols: break
        ua, i1a, i2a = wins[a]; ub, i1b, i2b = wins[b]
        ka, kb = (ua.path, ua.qual), (ub.path, ub.qual)
        if ka in flagged or kb in flagged: continue
        # persistence at +24m
        ok = True
        for (u, i1, i2) in ((ua, i1a, i2a), (ub, i1b, i2b)):
            lu = [x for x in units_of(repo, later, u.path, lang) if x.qual == u.qual]
            if not lu: ok = False; break
            w = u.norm[i1:i2]
            sm = SequenceMatcher(None, w, lu[0].norm[:3000], autojunk=False)
            if sm.find_longest_match(0, len(w), 0, min(len(lu[0].norm), 3000)).size < 0.8 * len(w): ok = False; break
        if not ok: continue
        ra, rb = ua.toks[i1a:i2a], ub.toks[i1b:i2b]
        na, nb = ua.norm[i1a:i2a], ub.norm[i1b:i2b]
        h, un = holes(ra, rb, na, nb)
        sites = []
        for (u, i1, i2) in ((ua, i1a, i2a), (ub, i1b, i2b)):
            stm = len({u.stmts[i] for i in range(i1, i2) if u.stmts[i] is not None}) or 1
            sites.append(dict(path=u.path, qual=u.qual, unit_lines=[u.sl, u.el], region_lines=[u.lines[i1], u.lines[i2 - 1]], ntok=i2 - i1, nstmt=stm))
        paths = [s['path'] for s in sites]
        out.append(dict(event_gid=e['gid'], repo=e['repo'], lang=e['lang'], parent=P, commit_ts=ts, sites=sites, n_sites=2,
                        ntok_min=min(s['ntok'] for s in sites), ntok_median=sum(s['ntok'] for s in sites) / 2, nstmt_min=min(s['nstmt'] for s in sites),
                        same_file=len(set(paths)) == 1, same_dir=len({os.path.dirname(p) for p in paths}) == 1,
                        cross_package=len({'/'.join(p.split('/')[:-1][:3]) for p in paths}) > 1,
                        test_only=test_only, sim_pair_max_raw=round(ratio(ra, rb), 3), sim_pair_max_shape=round(sr, 3), sim_pair_max_jacc5=round(jacc(ra, rb), 3),
                        holes=h, unaligned=un, label='negative', match_quality='relaxed' if RELAX else 'tight', follow_up_months=24, still_present_at_followup=True, follow_up_commit=later))
    return dict(censored=False, controls=out, n_pairs_evaluated=evaluated)

def work(e):
    rng = random.Random(hash(e['gid']) % (2**31) if False else sum(map(ord, e['gid'])))
    random.seed(sum(map(ord, e['gid'])))
    try:
        r = event_controls(e, rng)
    except Exception as ex:
        r = dict(censored=False, controls=[], error=repr(ex)[:120])
    return e['gid'], r

if __name__ == '__main__':
    ev = [json.loads(l) for l in open(sys.argv[1])]
    if len(sys.argv) > 3: ev = ev[:int(sys.argv[3])]
    with Pool(4) as p: res = p.map(work, ev, chunksize=1)
    with open(sys.argv[2], 'w') as f:
        for gid, r in res:
            for c in r['controls']: f.write(json.dumps(c) + '\n')
    meta = {gid: {k: v for k, v in r.items() if k != 'controls'} | {'n': len(r['controls'])} for gid, r in res}
    json.dump(meta, open(sys.argv[2] + '.meta.json', 'w'))
    print(sum(m['n'] for m in meta.values()), 'controls;', sum(1 for m in meta.values() if m.get('censored')), 'censored;',
          sum(1 for m in meta.values() if m['n'] == 0 and not m.get('censored')), 'zero-control uncensored;', sum(1 for m in meta.values() if 'error' in m), 'errors')
