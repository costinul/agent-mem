import os, json, urllib.request
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '..', '.env'))

API = os.environ.get('MEMORY_API_URL', 'http://localhost:8080').rstrip('/')
KEY = os.environ['MEMORY_API_KEY']
AGENT = os.environ['MEMORY_AGENT_ID']
THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'

questions = [
    "Where did Oliver hide his bone once?",
    "What activity did Caroline used to do with her dad?",
    "Which classical musicians does Melanie enjoy listening to?",
    "What was Melanie's favorite book from her childhood?",
    "What country is Caroline's grandma from?",
    "What was grandma's gift to Caroline?",
    "What does Caroline's necklace symbolize?",
]
for q in questions:
    body = json.dumps({'query': q, 'agent_id': AGENT, 'thread_id': THREAD, 'limit': 5}).encode()
    req = urllib.request.Request(
        API + '/memory/recall',
        data=body,
        headers={'Content-Type': 'application/json', 'X-API-Key': KEY},
        method='POST',
    )
    with urllib.request.urlopen(req, timeout=60) as r:
        data = json.load(r)
    print('=' * 80)
    print(f'Q: {q}')
    for f in data.get('facts', [])[:5]:
        print(f"  - {f.get('text','')[:140]}")
