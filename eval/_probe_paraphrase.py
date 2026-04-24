import os, json, urllib.request
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '..', '.env'))

API = os.environ.get('MEMORY_API_URL', 'http://localhost:8080').rstrip('/')
KEY = os.environ['MEMORY_API_KEY']
AGENT = os.environ['MEMORY_AGENT_ID']
THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'

probes = [
    ("What activity did Caroline used to do with her dad?", "Caroline's childhood activity with her father"),
    ("What activity did Caroline used to do with her dad?", "Caroline horseback riding"),
    ("What country is Caroline's grandma from?",            "Caroline grandmother's country of origin"),
    ("What was grandma's gift to Caroline?",                "necklace from Caroline's grandma"),
    ("What does Caroline's necklace symbolize?",            "meaning of Caroline's necklace"),
    ("Which classical musicians does Melanie enjoy listening to?", "Melanie classical music preferences"),
    ("What was Melanie's favorite book from her childhood?",       "Melanie's childhood favorite book"),
]
def call(q):
    body = json.dumps({'query': q, 'agent_id': AGENT, 'thread_id': THREAD, 'limit': 5}).encode()
    req = urllib.request.Request(API + '/memory/recall', data=body,
        headers={'Content-Type': 'application/json', 'X-API-Key': KEY}, method='POST')
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r).get('facts', [])

for orig, para in probes:
    print('=' * 80)
    print(f'ORIG : {orig}')
    for f in call(orig)[:5]:
        print(f"  [O] {f.get('text','')[:120]}")
    print(f'PARA : {para}')
    for f in call(para)[:5]:
        print(f"  [P] {f.get('text','')[:120]}")
