import os
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '..', '.env'))
import psycopg2
from openai import AzureOpenAI

THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'
client = AzureOpenAI(
    api_key=os.environ["AZURE_OPENAI_API_KEY"],
    azure_endpoint=os.environ.get("AZURE_OPENAI_ENDPOINT", "https://cchat-ai.cognitiveservices.azure.com/"),
    api_version="2024-02-15-preview",
)
def emb(t): return client.embeddings.create(model="text-embedding-3-small", input=[t]).data[0].embedding

conn = psycopg2.connect(
    host='agent-mem.postgres.database.azure.com', port=5432, dbname='agent-mem',
    user='agentmemadm', password='6dFAW49oXbNLNU8S3gqGLJKp8FTAAB2w', sslmode='require',
)
cur = conn.cursor()

# Check pgvector index probes / ef_search
for setting in ('ivfflat.probes', 'hnsw.ef_search'):
    try:
        cur.execute(f"SHOW {setting}")
        print(f'{setting} =', cur.fetchone())
    except Exception as e:
        print(f'{setting} not set:', str(e).strip())
        conn.rollback()

cur.execute("SELECT indexdef FROM pg_indexes WHERE tablename='facts' AND indexname LIKE '%embed%'")
for r in cur.fetchall():
    print('INDEX:', r[0])

# Score of the SPECIFIC target fact for each query
probes = [
    ("What country is Caroline's grandma from?",            "871cc5cc-a2e5-43ff-9645-eebf96fcfcaf"),
    ("What was grandma's gift to Caroline?",                "871cc5cc-a2e5-43ff-9645-eebf96fcfcaf"),
    ("What does Caroline's necklace symbolize?",            None),
    ("Which classical musicians does Melanie enjoy listening to?", None),
    ("What was Melanie's favorite book from her childhood?", None),
    ("What activity did Caroline used to do with her dad?", None),
    ("Where did Oliver hide his bone once?",                None),
]
# For ones without specific id, do a SEQUENTIAL scan across the WHOLE thread (no LIMIT, no index ANN)
# to find the true rank+score of the target text.
target_snippets = {
    "What does Caroline's necklace symbolize?":               "love, faith, and strength",
    "Which classical musicians does Melanie enjoy listening to?": "Bach and Mozart",
    "What was Melanie's favorite book from her childhood?":   "Charlotte",
    "What activity did Caroline used to do with her dad?":    "horseback riding with her dad",
    "Where did Oliver hide his bone once?":                   "slipper",
}

for q, fid in probes:
    e = emb(q)
    vec = "[" + ",".join(str(v) for v in e) + "]"
    if fid:
        cur.execute(f"""
            SELECT 1 - (embedding <=> '{vec}'::vector) AS score, text
            FROM facts WHERE id = %s
        """, (fid,))
        row = cur.fetchone()
        print(f"\nQ: {q!r}")
        print(f"   target  : score={round(row[0],4):>6}  {row[1][:90]}")
    snip = target_snippets.get(q)
    if not snip:
        continue
    # Find rank by full-thread scan (bypassing index)
    cur.execute(f"""
        WITH ranked AS (
            SELECT id, text,
                   1 - (embedding <=> '{vec}'::vector) AS score,
                   ROW_NUMBER() OVER (ORDER BY embedding <=> '{vec}'::vector ASC) AS rk
            FROM facts
            WHERE thread_id = %s AND superseded_at IS NULL AND embedding IS NOT NULL
        )
        SELECT rk, score, text FROM ranked WHERE text ILIKE %s
    """, (THREAD, f"%{snip}%"))
    rows = cur.fetchall()
    print(f"\nQ: {q!r}  snippet={snip!r}")
    for rk, sc, t in rows[:5]:
        print(f"   rank={rk:3d}  score={round(sc,4):>6}  {t[:90]}")

conn.close()
