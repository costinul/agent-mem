"""Use the exact stored fact text as the recall query to isolate DecomposeRecall vs SelectFacts."""
import os
import urllib.request
import json
import time
from pathlib import Path
from dotenv import load_dotenv
load_dotenv(Path(__file__).resolve().parent.parent.parent / ".env")

API = os.environ.get("MEMORY_API_URL", "http://localhost:8080") + "/memory/recall"
THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'
AGENT  = '640fef54-85e8-4174-a2bb-8822c84b606a'
KEY    = os.environ["MEMORY_API_KEY"]


def recall(q):
    body = json.dumps({'thread_id': THREAD, 'agent_id': AGENT, 'query': q}).encode()
    req = urllib.request.Request(
        API, data=body,
        headers={'Authorization': 'Bearer ' + KEY, 'Content-Type': 'application/json'}
    )
    facts = json.loads(urllib.request.urlopen(req, timeout=60).read())['facts']
    return [f['text'] for f in facts]


# Query = exact stored fact text.
# If recall returns 0 facts, DecomposeRecall is losing it (MISS_C via decompose).
# If recall returns >=1 fact, it's a MISS_E for the original phrasing.
exact_tests = [
    ("exact-grandma",   "The necklace was a gift from Caroline's grandma in Sweden.",  "grandma in Sweden"),
    ("exact-figurine",  "Melanie bought figurines yesterday that remind her of family love.", "figurines"),
    ("exact-bach",      "Melanie is a fan of classical music like Bach and Mozart.",    "Bach and Mozart"),
    ("exact-oliver",    "Oliver hid his bone in Melanie's slipper once.",               "slipper"),
    ("exact-charlotte", 'Melanie\'s favorite childhood book is "Charlotte\'s Web,"',    "Charlotte"),
    ("exact-horseback", "Caroline used to go horseback riding with her dad when she was a kid.", "horseback"),
    ("exact-necklace",  "The necklace stands for love, faith, and strength.",           "necklace symboliz"),
    ("exact-charity",   "Melanie ran a charity race for mental health last Saturday.",  "charity"),
]

print(f"{'Label':<22} {'N':>3}  Classification")
print("-" * 65)
for label, exact_query, snippet in exact_tests:
    facts = recall(exact_query)
    n = len(facts)
    hit = any(snippet.lower() in f.lower() for f in facts)
    if n == 0:
        kind = "MISS_C via DecomposeRecall  (exact text query still returns nothing)"
    elif hit:
        kind = f"MISS_E  (exact returns {n} facts incl. target; original failed)"
    else:
        kind = f"MISS_E? (exact returns {n} facts but NOT the target one)"
    print(f"{label:<22} {n:>3}  {kind}")
    if facts:
        for f in facts[:2]:
            print(f"  -> {f[:90]}")
    time.sleep(0.3)
