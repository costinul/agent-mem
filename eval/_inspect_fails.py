import json
d = json.load(open('results_after_fix.json', encoding='utf-8'))
qa = [q for c in d['conversations'] for q in c.get('qa', [])]
targets = [
    "Where did Oliver hide his bone once?",
    "What activity did Caroline used to do with her dad?",
    "Which  classical musicians does Melanie enjoy listening to?",
    "What was Melanie's favorite book from her childhood?",
    "What country is Caroline's grandma from?",
    "What was grandma's gift to Caroline?",
    "What does Caroline's necklace symbolize?",
    "What did Caroline see at the council meeting for adoption?",
    "When did Melanie buy the figurines?",
    "What setback did Melanie face in October 2023?",
]
for q in qa:
    if q['question'] in targets:
        print('='*80)
        print(f"Q: {q['question']}")
        print(f"GT: {q.get('ground_truth')}")
        print(f"Score: {q.get('score')}  Reason: {q.get('reason','')}")
        for f in (q.get('facts_returned') or [])[:6]:
            t = f if isinstance(f, str) else f.get('text', str(f))
            print(f"  - {t[:140]}")
