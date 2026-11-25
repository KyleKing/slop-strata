import sys, json, os
sys.path.insert(0, os.path.dirname(__file__))
from multiprocessing import Pool
import age
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
def work(c):
    repo = f"{E}/repos/{c['repo']}"
    sites = [dict(path=s['path'], region_lines=s['region_lines']) for s in c['sites']]
    try:
        h = age.history(repo, c['parent'], c['commit_ts'], sites); h.update(age.prior_stats(repo, c['parent'], c['commit_ts'], sites))
    except Exception as ex: h = dict(error=repr(ex)[:100])
    c = dict(c); c['hist'] = h; return c
if __name__ == '__main__':
    cs = [json.loads(l) for l in open(sys.argv[1])]
    with Pool(4) as p: out = p.map(work, cs, chunksize=4)
    with open(sys.argv[2], 'w') as f:
        for c in out: f.write(json.dumps(c) + '\n')
    print(len(out), sum(1 for c in out if c['hist'].get('n_aged', 0) >= 2))
