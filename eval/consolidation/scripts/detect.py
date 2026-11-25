"""Structural consolidation-candidate detector. Usage: detect.py REPO_DIR OUT_DIR [start_idx stride]"""
import sys, os, json, subprocess, itertools, time
sys.path.insert(0, os.path.dirname(__file__))
from difflib import SequenceMatcher
import units as U
from sim import ratio, jacc, holes, pair_metrics

MIN_REM = 8        # min removed tokens for a site region
SHAPE_FLOOR = 0.40 # candidate floor on pairwise shape similarity (recorded; deliberately loose)
MAX_FILES = 30
MAX_BLOB = 300_000
SKIP_PARTS = ('node_modules/', 'vendor/', 'dist/', 'build/', 'third_party/', '/testdata/', 'fixtures/', 'locale/', 'locales/', '.min.')
MSG_WORDS = ['dedup', 'duplicat', 'dry', 'extract common', 'extract ', 'share', 'shared', 'reuse', 're-use', 'consolidat',
             'factor out', 'factor', 'pull up', 'pull-up', 'unify', 'unif', 'refactor', 'common', 'merge ', 'de-dup', 'combine', 'helper']
MSG_STRONG = ['dedup', 'de-dup', 'duplicat', 'dry', 'extract common', 'consolidat', 'factor out', 'pull up', 'pull-up', 'unify', 'reuse', 're-use', 'share', 'shared', 'common', 'combine', 'extract']

class Git:
    def __init__(self, repo):
        self.repo = repo
        self.p = subprocess.Popen(['git', '-C', repo, 'cat-file', '--batch'], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    def blob(self, sha):
        if not sha or set(sha) == {'0'}: return None
        self.p.stdin.write((sha + '\n').encode()); self.p.stdin.flush()
        hdr = self.p.stdout.readline().split()
        if len(hdr) < 3 or hdr[1] != b'blob':
            return None
        n = int(hdr[2]); data = self.p.stdout.read(n); self.p.stdout.read(1)
        return data

def commits(repo):
    out = subprocess.run(['git', '-C', repo, '-c', 'core.quotepath=false', 'log', '--no-merges', '-M', '--raw', '--abbrev=40',
                          '--format=@@%H %P|%ct|%s', '--reverse'], capture_output=True, text=True, errors='replace').stdout
    cur = None
    for line in out.split('\n'):
        if line.startswith('@@'):
            if cur: yield cur
            h, rest = line[2:].split(' ', 1)
            par, ct, subj = rest.split('|', 2)
            cur = dict(sha=h, parent=(par.split() or [None])[0], ts=int(ct), subj=subj, files=[])
        elif line.startswith(':') and cur is not None:
            meta, *paths = line.split('\t')
            m = meta.split()
            st = m[4]
            cur['files'].append(dict(status=st[0], old_sha=m[2], new_sha=m[3], old_path=paths[0], new_path=paths[-1]))
    if cur: yield cur

def is_test(path):
    p = path.lower()
    return ('test' in p.split('/')[-1]) or '/tests/' in '/' + p or '/test/' in '/' + p or '__tests__' in p or p.endswith('.test.ts') or p.endswith('.spec.ts')

def merge_ops(ops, gap=3):
    """Merge change opcodes separated by short equal runs."""
    res = []
    for tag, i1, i2, j1, j2 in ops:
        if tag == 'equal': continue
        if res and i1 - res[-1][1] <= gap and j1 - res[-1][3] <= gap:
            r = res[-1]; res[-1] = (r[0], i2, r[2], j2)
        else:
            res.append((i1, i2, j1, j2))
    return [(i1, i2, j1, j2) for i1, i2, j1, j2 in res] if res and len(res[0]) == 4 else res

def merge_ops2(ops, gap=3):
    res = []
    for tag, i1, i2, j1, j2 in ops:
        if tag == 'equal': continue
        if res and i1 - res[-1][1] <= gap and j1 - res[-1][3] <= gap:
            res[-1] = (res[-1][0], i2, res[-1][2], j2)
        else:
            res.append((i1, i2, j1, j2))
    return res

def diff_ops(a, b):
    p = 0
    while p < len(a) and p < len(b) and a[p] == b[p]: p += 1
    q = 0
    while q < len(a) - p and q < len(b) - p and a[-1 - q] == b[-1 - q]: q += 1
    sm = SequenceMatcher(None, a[p:len(a) - q], b[p:len(b) - q], autojunk=False)
    return [(t, i1 + p, i2 + p, j1 + p, j2 + p) for t, i1, i2, j1, j2 in sm.get_opcodes()]

def short(q): return q.split('.')[-1]

import builtins as _b
STOP = set(dir(_b)) | {'self', 'cls', 'this', 'super', 'undefined', 'console', 'Object', 'Array', 'String', 'Number', 'Promise', 'Error', 'JSON', 'Math',
        'len', 'append', 'make', 'new', 'string', 'error', 'nil', 'fmt', 'Errorf', 'Sprintf', 'args', 'kwargs', 'length', 'push', 'map', 'filter', 'then',
        'value', 'values', 'keys', 'key', 'name', 'data', 'result', 'get', 'set', 'add', 'has', 'call', 'apply', 'bind', 'join', 'split', 'slice', 'name', 'type',
        'Error', 'return', 'async', 'await', 'catch', 'ok', 'err', 'ctx', 'req', 'res', 'next', 'item', 'items', 'index', 'node', 'state', 'options', 'opts'}

def moved(u, added):
    for a in added:
        if len(a.norm) and abs(len(a.norm) - len(u.norm)) <= 0.2 * len(u.norm) and ratio(u.norm[:300], a.norm[:300]) >= 0.9 and ratio(u.toks[:300], a.toks[:300]) >= 0.85:
            return True
    return False

def analyse(gi, c, lang_repo):
    """Return list of candidate dicts for commit c."""
    files = [f for f in c['files'] if U.lang_for(f['new_path']) and not any(s in f['new_path'] for s in SKIP_PARTS)
             and not f['new_path'].endswith('.d.ts')]
    if not files: return None, 0
    if len([f for f in c['files']]) > MAX_FILES: return None, len(files)
    old_units = {}; new_units = {}; fstatus = {}
    for f in files:
        lang = U.lang_for(f['new_path'])
        so = gi.blob(f['old_sha']) if f['status'] != 'A' else None
        sn = gi.blob(f['new_sha']) if f['status'] != 'D' else None
        if (so and len(so) > MAX_BLOB) or (sn and len(sn) > MAX_BLOB): continue
        fstatus[f['new_path']] = f['status']
        if so is not None:
            for u in U.extract(so, lang, f['new_path'])[0]: old_units[(u.path, u.qual)] = u
        if sn is not None:
            for u in U.extract(sn, lang, f['new_path'])[0]: new_units[(u.path, u.qual)] = u
    cands = []
    # ---- per matched unit: removed regions + added tokens
    added = {k: u for k, u in new_units.items() if k not in old_units and u.kind != 'class'}
    deleted = {k: u for k, u in old_units.items() if k not in new_units and u.kind != 'class'}
    sites = []   # (unit_old, unit_new, region(i1,i2,j1,j2), gained idents)
    grown = {}
    for k, uo in old_units.items():
        un = new_units.get(k)
        if un is None or uo.kind == 'class' or uo.toks == un.toks: continue
        if len(uo.toks) > 8000 or len(un.toks) > 8000: continue
        ops2 = merge_ops2(diff_ops(uo.toks, un.toks))
        ins = sum(j2 - j1 for i1, i2, j1, j2 in ops2)
        rem = sum(i2 - i1 for i1, i2, j1, j2 in ops2)
        if ins - rem >= 6 and ins >= 8: grown[k] = un
        for (i1, i2, j1, j2) in ops2:
            if i2 - i1 >= MIN_REM and (j2 - j1) <= 0.6 * (i2 - i1):
                rem_set = set(uo.toks[i1:i2])
                gained = {t for t, n in zip(un.toks[j1:j2], un.norm[j1:j2]) if n == 'ID' and t not in rem_set and len(t) >= 2}
                sites.append(dict(uo=uo, un=un, reg=(i1, i2, j1, j2), gained=gained))
    xs = {}
    for k, u in list(added.items()) + list(grown.items()):
        if u.kind in ('func', 'const'): xs.setdefault(short(u.qual), []).append((k, u))
    # ---- group sites by gained identifier
    by_g = {}
    for s in sites:
        for g in s['gained']:
            by_g.setdefault(g, []).append(s)
    for g, ss in by_g.items():
        # distinct units
        seen = {}
        for s in ss:
            key = (s['uo'].path, s['uo'].qual)
            if key not in seen or (s['reg'][1] - s['reg'][0]) > (seen[key]['reg'][1] - seen[key]['reg'][0]): seen[key] = s
        ss = list(seen.values())
        if len(ss) < 2: continue
        xk = xs.get(g)
        if not xk and g in STOP: continue
        if not xk:
            # unresolved: require stricter handling later
            xinfo = None
        else:
            xinfo = xk[0]
        ss.sort(key=lambda s: -(s['reg'][1] - s['reg'][0])); ss = ss[:10]
        regs = [(s['uo'].toks[s['reg'][0]:s['reg'][1]], s['uo'].norm[s['reg'][0]:s['reg'][1]]) for s in ss]
        pm = {}
        for a, b in itertools.combinations(range(len(ss)), 2):
            pm[(a, b)] = ratio(regs[a][1], regs[b][1])
        good = set()
        for (a, b), v in pm.items():
            if v >= SHAPE_FLOOR + (0.15 if xinfo is None else 0): good.add(a); good.add(b)
        if len(good) < 2: continue
        gl = sorted(good)
        cands.append(dict(kind='extract', g=g, xinfo=xinfo, sites=[ss[i] for i in gl], regs=[regs[i] for i in gl],
                          pm={(gl.index(a), gl.index(b)): v for (a, b), v in pm.items() if a in good and b in good}))
    # ---- pull-up: same-named method deleted from >=2 classes, gained by another class
    del_by_name = {}
    addl = [u for u in added.values() if u.kind == 'func']
    for k, u in deleted.items():
        if u.kind == 'func' and '.' in u.qual and not moved(u, addl) and fstatus.get(u.path) != 'D' and U.lang_for(u.path) != 'go':
            del_by_name.setdefault((short(u.qual)), []).append((k, u))
    for nm, lst in del_by_name.items():
        if nm in ('__init__', 'constructor', '__repr__', '__str__') and len(lst) < 2: continue
        if len({k[1].rsplit('.', 1)[0] for k, u in lst if True} | {k[0] for k, u in lst}) < 2: continue
        tg = [(k, u) for k, u in list(added.items()) + list(grown.items()) if u.kind == 'func' and short(u.qual) == nm and '.' in u.qual]
        if not tg: continue
        regs = [(u.toks[:400], u.norm[:400]) for k, u in lst]
        okp = [(a, b) for a, b in itertools.combinations(range(len(lst)), 2) if ratio(regs[a][1], regs[b][1]) >= SHAPE_FLOOR]
        if not okp: continue
        good = sorted({i for p in okp for i in p})
        cands.append(dict(kind='pullup', g=nm, xinfo=tg[0], sites=[dict(uo=lst[i][1], un=None, reg=(0, len(lst[i][1].toks), 0, 0)) for i in good],
                          regs=[regs[i] for i in good], pm={}))
    # ---- merge-delete: >=2 whole functions deleted (file survives), similar to each other and to an added/grown func
    dl = [(k, u) for k, u in deleted.items() if u.kind == 'func' and len(u.toks) >= MIN_REM and fstatus.get(u.path) not in ('D', 'R') and not moved(u, addl)]
    if len(dl) >= 2:
        tgt = [(k, u) for k, u in list(added.items()) + list(grown.items()) if u.kind == 'func']
        for k2, u2 in tgt[:6]:
            sim = [(ratio(u.norm[:400], u2.norm[:400]), k, u) for k, u in dl[:12]]
            sim = [x for x in sim if x[0] >= 0.5]
            if len(sim) >= 2:
                cands.append(dict(kind='merge_delete', g=short(k2[1]), xinfo=(k2, u2),
                                  sites=[dict(uo=u, un=None, reg=(0, len(u.toks), 0, 0)) for _, k, u in sim],
                                  regs=[(u.toks[:400], u.norm[:400]) for _, k, u in sim], pm={}))
    return cands, len(files)

def render(c, cand, lang_repo, repo_name):
    sites = []
    for s, (rt, rn) in zip(cand['sites'], cand['regs']):
        uo = s['uo']; i1, i2, j1, j2 = s['reg']
        stm = len({uo.stmts[i] for i in range(i1, min(i2, len(uo.stmts))) if uo.stmts[i] is not None}) or 1
        sites.append(dict(path=uo.path, qual=uo.qual, unit_lines=[uo.sl, uo.el], region_lines=[uo.lines[i1] if uo.lines else uo.sl, uo.lines[i2-1] if uo.lines else uo.el],
                          ntok=i2 - i1, nstmt=stm, ntok_new=(j2 - j1), unit_ntok=len(uo.toks)))
    # similarity stats over all pairs
    n = len(rt_list := cand['regs'])
    pairs = []
    for a, b in itertools.combinations(range(min(n, 8)), 2):
        m = pair_metrics(rt_list[a][0][:400], rt_list[a][1][:400], rt_list[b][0][:400], rt_list[b][1][:400])
        h, un = holes(rt_list[a][0], rt_list[b][0], rt_list[a][1], rt_list[b][1])
        m['holes'] = h; m['unaligned'] = un
        pairs.append(m)
    x = None; xs = {}
    if cand['xinfo']:
        k, xu = cand['xinfo']
        x = dict(path=xu.path, qual=xu.qual, kind=xu.kind, lines=[xu.sl, xu.el], ntok=len(xu.toks))
        xs = max((ratio(xu.norm[:400], rn) for rt, rn in cand['regs']), default=0)
    paths = [s['path'] for s in sites]
    return dict(repo=repo_name, sha=c['sha'], parent=c['parent'], ts=c['ts'], subj=c['subj'], nfiles=len(c['files']), kind=cand['kind'], ident=cand['g'],
                x=x, x_sim_to_sites=round(xs, 3) if x else None, sites=sites, n_sites=len(sites),
                pair_max_shape=max((p['shape'] for p in pairs), default=0), pair_max_raw=max((p['raw'] for p in pairs), default=0),
                pair_max_jacc5=max((p['jacc5'] for p in pairs), default=0), pair_min_shape=min((p['shape'] for p in pairs), default=0),
                pairs=pairs[:6], same_file=len(set(paths)) == 1, same_dir=len({os.path.dirname(p) for p in paths}) == 1,
                all_test=all(is_test(p) for p in paths), any_test=any(is_test(p) for p in paths))

def msg_flag(subj):
    s = subj.lower()
    return [w for w in MSG_STRONG if w in s]

def main():
    repo = sys.argv[1]; out = sys.argv[2]
    lang_repo = sys.argv[3] if len(sys.argv) > 3 else ''
    name = os.path.basename(repo.rstrip('/'))
    gi = Git(repo)
    os.makedirs(out, exist_ok=True)
    fc = open(f'{out}/{name}.cands.jsonl', 'w'); fm = open(f'{out}/{name}.commits.jsonl', 'w')
    t0 = time.time(); n = 0
    for c in commits(repo):
        n += 1
        try:
            cands, nf = analyse(gi, c, lang_repo)
        except Exception as e:
            cands, nf = None, -1
            print('ERR', c['sha'], repr(e)[:200], file=sys.stderr)
        mf = msg_flag(c['subj'])
        fm.write(json.dumps(dict(sha=c['sha'], ts=c['ts'], subj=c['subj'], nsrc=nf, nfiles=len(c['files']), msg=mf, ncand=len(cands) if cands else 0)) + '\n')
        for cd in cands or []:
            fc.write(json.dumps(render(c, cd, lang_repo, name)) + '\n')
        if n % 500 == 0:
            print(name, n, round(time.time() - t0), flush=True); fc.flush(); fm.flush()
    fc.close(); fm.close(); print('DONE', name, n, round(time.time() - t0))

if __name__ == '__main__':
    main()
