"""Quick script to test verbatim vs natural queries for MISS_C vs MISS_E distinction."""
import urllib.request
import json
import time

API = 'http://localhost:8080/memory/recall'
THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'
AGENT = '640fef54-85e8-4174-a2bb-8822c84b606a'
KEY = 'amk_f923aad4b84a_c8437dd9a034186030b136d1eb65d4ed8953dcd78f99de45'


def recall(q):
    body = json.dumps({'thread_id': THREAD, 'agent_id': AGENT, 'query': q}).encode()
    req = urllib.request.Request(
        API, data=body,
        headers={'Authorization': 'Bearer ' + KEY, 'Content-Type': 'application/json'}
    )
    facts = json.loads(urllib.request.urlopen(req, timeout=60).read())['facts']
    return [f['text'] for f in facts]


# Each tuple: (label, verbatim_query_close_to_stored_fact, original_failing_question)
tests = [
    (
        'oliver-slipper',
        'Oliver hid his bone in Melanie slipper',
        'Where did Oliver hide his bone once?',
    ),
    (
        'grandma-sweden',
        'Caroline grandma is from Sweden',
        "What country is Caroline's grandma from?",
    ),
    (
        'charlotte-web',
        "Melanie's favorite childhood book is Charlotte's Web",
        "What was Melanie's favorite book from her childhood?",
    ),
    (
        'trans-lives-poster',
        'Trans Lives Matter poster at poetry reading',
        'What did the posters at the poetry reading say?',
    ),
    (
        'horseback-dad',
        'Caroline used to go horseback riding with her dad',
        'What activity did Caroline used to do with her dad?',
    ),
    (
        'figurine-bought',
        'Melanie bought figurines',
        'When did Melanie buy the figurines?',
    ),
    (
        'necklace-symbolize',
        'Caroline necklace symbolizes love faith strength',
        "What does Caroline's necklace symbolize?",
    ),
    (
        'charity-race',
        'Melanie ran a charity race',
        'When did Melanie run a charity race?',
    ),
    (
        'becoming-nicole',
        'Caroline recommended the book Becoming Nicole to Melanie',
        'What book did Caroline recommend to Melanie?',
    ),
    (
        'bach-mozart',
        'Melanie enjoys listening to Bach and Mozart classical music',
        'Which classical musicians does Melanie enjoy listening to?',
    ),
]

print(f"{'Label':<22} {'Verbatim recall':>15} {'Original recall':>15}")
print("-" * 55)
for label, verbatim_q, original_q in tests:
    v_facts = recall(verbatim_q)
    time.sleep(0.3)
    o_facts = recall(original_q)
    time.sleep(0.3)

    v_n = len(v_facts)
    o_n = len(o_facts)

    if v_n > 0 and o_n == 0:
        kind = 'MISS_E  (SelectFacts too strict on orig question)'
    elif v_n == 0 and o_n == 0:
        kind = 'MISS_C  (vector search misses even verbatim)'
    elif v_n > 0 and o_n > 0:
        kind = 'OK both'
    else:
        kind = 'MISS_E? (orig returned, verbatim did not)'

    print(f"{label:<22} {v_n:>15} {o_n:>15}   {kind}")
    if v_facts:
        print(f"  verbatim top: {v_facts[0][:80]}")
    if o_facts:
        print(f"  original top: {o_facts[0][:80]}")
