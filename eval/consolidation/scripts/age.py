"""Aging + history metrics for a set of sites at a parent commit.
site_history(repo_dir, parent, sites[{path,region_lines}]) -> per-site and pairwise metrics. No author identities stored."""
import subprocess, collections

def _git(repo, *a):
    return subprocess.run(['git', '-C', repo, '-c', 'core.quotepath=false'] + list(a), capture_output=True, text=True, errors='replace').stdout

def blame(repo, rev, path, a, b):
    out = _git(repo, 'blame', '-w', '-M', '--porcelain', '-L', f'{a},{b}', rev, '--', path)
    info = {}; counts = collections.Counter(); cur = None
    for line in out.split('\n'):
        if not line: continue
        if line[0] == '\t': counts[cur] += 1; continue
        parts = line.split(' ')
        if len(parts[0]) == 40 and all(c in '0123456789abcdef' for c in parts[0]):
            cur = parts[0]; info.setdefault(cur, {})
        elif parts[0] == 'author-time': info[cur]['t'] = int(parts[1])
        elif parts[0] == 'author-mail': info[cur]['a'] = parts[1]
    return info, counts

def site_age(repo, rev, path, a, b):
    info, counts = blame(repo, rev, path, a, b)
    if not counts: return None
    dom = counts.most_common(1)[0][0]
    ts = [info[c]['t'] for c in counts]
    return dict(dom=dom, dom_t=info[dom]['t'], dom_a=info[dom].get('a'), first_t=min(ts), last_t=max(ts), commits=set(counts), ncommits=len(counts))

def rev_count(repo, rev, path, since=None):
    args = ['rev-list', '--count', rev]
    if since: args.append(f'--since={since}')
    args += ['--', path]
    try: return int(_git(repo, *args).strip() or 0)
    except ValueError: return 0

def cochange(repo, rev, pa, pb):
    if pa == pb: return rev_count(repo, rev, pa)
    A = set(_git(repo, 'rev-list', rev, '--', pa).split()); B = set(_git(repo, 'rev-list', rev, '--', pb).split())
    return len(A & B)

def history(repo, parent, parent_ts, sites):
    """sites: list of dict(path, region_lines). returns dict of aggregated metrics."""
    ages = []
    for s in sites[:6]:
        a = site_age(repo, parent, s['path'], s['region_lines'][0], s['region_lines'][1])
        ages.append(a)
    ok = [a for a in ages if a]
    res = dict(n_aged=len(ok))
    if len(ok) < 2: return res
    # sort sites by dominant-commit time
    ok.sort(key=lambda a: a['dom_t'])
    early, late = ok[0], ok[-1]
    res['birth_spread_days'] = round((late['dom_t'] - early['dom_t']) / 86400, 1)
    res['dup_lifetime_days'] = round((parent_ts - late['dom_t']) / 86400, 1)        # since the last site appeared
    res['oldest_site_age_days'] = round((parent_ts - early['dom_t']) / 86400, 1)
    res['same_birth_commit'] = len({a['dom'] for a in ok}) == 1
    res['later_site_after_earlier'] = (late['dom'] != early['dom']) and (late['dom_t'] > early['dom_t'])
    res['same_author_as_earlier'] = (late['dom_a'] == early['dom_a']) if late['dom_a'] and early['dom_a'] else None
    shared = set.intersection(*[a['commits'] for a in ok]) - {a['dom'] for a in ok}
    res['lockstep_commits'] = len(shared)                                              # commits (other than births) still visible in blame of ALL sites
    solo = 0
    for a in ok:
        others = set.union(*[b['commits'] for b in ok if b is not a])
        solo += len(a['commits'] - others - {a['dom']})
    res['solo_edit_commits'] = solo
    res['diverged'] = solo > 0
    res['n_commits_in_region_blame'] = [a['ncommits'] for a in ok]
    return res

def prior_stats(repo, parent, parent_ts, sites):
    paths = sorted({s['path'] for s in sites})
    since = f'@{parent_ts - 365*86400}'
    churn = sum(rev_count(repo, parent, p, since=None) for p in paths) / max(1, len(paths))
    recent = sum(rev_count(repo, parent, p, since=since) for p in paths) / max(1, len(paths))
    co = cochange(repo, parent, paths[0], paths[-1]) if len(paths) >= 1 else 0
    return dict(prior_commits_per_file=round(churn, 1), prior_commits_12m_per_file=round(recent, 1), cochange_commits=co)
