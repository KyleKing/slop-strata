import sys, json, os
sys.path.insert(0, os.path.dirname(__file__))
from multiprocessing import Pool
import age
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
def work(e):
    repo = f"{E}/repos/{e['repo']}"
    sites = [dict(path=s['path'], region_lines=s['region_lines']) for s in e['sites']]
    try:
        h = age.history(repo, e['parent'], e['commit_ts'], sites)
        h.update(age.prior_stats(repo, e['parent'], e['commit_ts'], sites))
    except Exception as ex:
        h = dict(error=repr(ex)[:100])
    e = dict(e); e['hist'] = h
    return e
if __name__ == '__main__':
    src, dst = sys.argv[1], sys.argv[2]
    ev = [json.loads(l) for l in open(src)]
    with Pool(4) as p: out = p.map(work, ev, chunksize=2)
    with open(dst, 'w') as f:
        for e in out: f.write(json.dumps(e) + '\n')
    print(len(out), sum(1 for e in out if e['hist'].get('n_aged', 0) >= 2), sum(1 for e in out if 'error' in e['hist']))
