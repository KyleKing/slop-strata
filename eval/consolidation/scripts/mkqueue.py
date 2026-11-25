import json, random, collections
random.seed(7)
G=[json.loads(l) for l in open('data/groups.jsonl')]
def stratum(g):
    k=g['kinds']
    if 'pullup' in k: return 'S3_pullup'
    if k==['extract']: return 'S1_extract_res' if g['resolved'] else 'S2_extract_unres'
    return 'S4_merge_delete'
for g in G: g['stratum']=stratum(g)
by=collections.defaultdict(list)
for g in G: by[g['stratum']].append(g)
print({k:len(v) for k,v in by.items()})
q=[]
for k,v in by.items():
    random.shuffle(v)
    for i,g in enumerate(v): q.append(dict(gid=g['gid'],stratum=k,rank=i))
json.dump(q,open('data/queue.json','w'))
