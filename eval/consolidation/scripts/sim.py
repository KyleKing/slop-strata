"""Token similarity helpers."""
from difflib import SequenceMatcher

def ratio(a, b, cap=500):
    a = a[:cap]; b = b[:cap]
    if not a or not b: return 0.0
    sm = SequenceMatcher(None, a, b, autojunk=False)
    if sm.real_quick_ratio() < 0.2: return 0.0
    return sm.ratio()

def shingles(t, n=5):
    if len(t) < n: return {tuple(t)} if t else set()
    return {tuple(t[i:i+n]) for i in range(len(t)-n+1)}

def jacc(a, b, n=5):
    A, B = shingles(a, n), shingles(b, n)
    if not A or not B: return 0.0
    return len(A & B) / len(A | B)

def holes(raw_a, raw_b, norm_a, norm_b, cap=500):
    """Number of aligned positions where normalised tokens agree but raw tokens differ (ID/LIT holes),
    and number of tokens not aligned at all (structural diff)."""
    raw_a = raw_a[:cap]; raw_b = raw_b[:cap]; norm_a = norm_a[:cap]; norm_b = norm_b[:cap]
    sm = SequenceMatcher(None, norm_a, norm_b, autojunk=False)
    h = 0; un = 0
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag == 'equal':
            for k in range(i2 - i1):
                if raw_a[i1+k] != raw_b[j1+k]: h += 1
        else:
            un += max(i2 - i1, j2 - j1)
    return h, un

def pair_metrics(a_raw, a_norm, b_raw, b_norm):
    return dict(raw=round(ratio(a_raw, b_raw), 3), shape=round(ratio(a_norm, b_norm), 3),
                jacc5=round(jacc(a_raw, b_raw), 3), jacc5_shape=round(jacc(a_norm, b_norm), 3))
