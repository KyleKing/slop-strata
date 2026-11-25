"""Attach holes/message flags/censoring to events; write events.jsonl, controls.jsonl (final)."""
import json, os, glob, collections
E = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MSG = ['dedup', 'de-dup', 'duplicat', 'dry', 'extract common', 'consolidat', 'factor out', 'pull up', 'pull-up', 'unify', 'reuse', 're-use', 'share', 'shared', 'common', 'combine', 'extract', 'helper']
ev = [json.loads(l) for l in open(f'{E}/events_hist.jsonl')]
cands = collections.defaultdict(list)
for f in glob.glob(f'{E}/data/*.cands.jsonl'):
    for l in open(f):
        c = json.loads(l); cands[(c['repo'], c['sha'])].append(c)
meta = json.load(open(f'{E}/controls_raw.jsonl.meta.json')); meta2 = json.load(open(f'{E}/controls_relaxed.jsonl.meta.json'))
for e in ev:
    keys = {(s['path'], s['qual']) for s in e['sites']}
    best = None
    for c in cands[(e['repo'], e['sha'])]:
        if not any((s['path'], s['qual']) in keys for s in c['sites']): continue
        for p in c['pairs']:
            if best is None or p['shape'] > best['shape'] or (p['shape'] == best['shape'] and p['raw'] > best['raw']): best = p
    e['sim_best_pair'] = best
    e['holes_best_pair'] = best['holes'] if best else None
    e['unaligned_best_pair'] = best['unaligned'] if best else None
    e['confidence'] = 'borderline' if any(k in e['note'].lower() for k in ['borderline', 'weak', 'low similarity', 'partially read', 'low confidence']) else 'clear'
    e['message_flags'] = [w for w in MSG if w in e['subj'].lower()]
    e['censored_for_controls'] = bool(meta.get(e['gid'], {}).get('censored'))
    e['n_controls'] = meta.get(e['gid'], {}).get('n', 0) + meta2.get(e['gid'], {}).get('n', 0)
    # unit-per-class sibling-method flag
    pre = {s['qual'].rsplit('.', 1)[0] for s in e['sites'] if '.' in s['qual']}
    e['sibling_class_methods'] = len(pre) >= 2 and all('.' in s['qual'] for s in e['sites'])
with open(f'{E}/events.jsonl', 'w') as f:
    for e in ev: f.write(json.dumps(e) + '\n')
import shutil; shutil.copy(f'{E}/controls.jsonl', f'{E}/controls_final.jsonl')
print(len(ev), sum(1 for e in ev if e['holes_best_pair'] is None))
