import json, collections, sys

def load(p):
    d = json.load(open(p, encoding='utf-8'))
    qa = [q for c in d['conversations'] for q in c.get('qa', [])]
    sc = collections.Counter(q.get('score', '?') for q in qa)
    return sc, sum(sc.values()) or 1, qa

before, nb, qb = load('results.json')
after,  na, qa = load('results_after_fix.json')
print(f'total before={nb}  after={na}')
for k in ('pass', 'partial', 'fail'):
    b = before.get(k, 0); a = after.get(k, 0)
    print(f'  {k:8s} before={b:3d} ({100*b/nb:5.1f}%)  after={a:3d} ({100*a/na:5.1f}%)  delta={a-b:+d}')

# Per-question delta on questions that were `fail` before
prev_fail = {q['question']: q['score'] for q in qb if q.get('score') == 'fail'}
flips = collections.Counter()
for q in qa:
    if q['question'] in prev_fail:
        flips[q.get('score', '?')] += 1
print('\nWas-FAIL transitions (n={}):'.format(len(prev_fail)))
for k, v in flips.most_common():
    print(f'  fail -> {k:8s} : {v}')
