import os, json
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

def get_embedding(text):
    return client.embeddings.create(model="text-embedding-3-small", input=[text]).data[0].embedding

conn = psycopg2.connect(os.environ["POSTGRES_DSN"])
cur = conn.cursor()

# 1. Verify the necklace fact exists
cur.execute("""
    SELECT id, text, account_id, agent_id, thread_id
    FROM facts
    WHERE thread_id = %s AND text ILIKE %s AND superseded_at IS NULL
""", (THREAD, '%necklace%grandma%Sweden%'))
print("== NECKLACE FACT ==")
for row in cur.fetchall():
    print(row)

# 2. Top-25 ANN with the verbatim question
queries = [
    "What country is Caroline's grandma from?",
    "What was grandma's gift to Caroline?",
    "What does Caroline's necklace symbolize?",
    "Which classical musicians does Melanie enjoy listening to?",
]
for q in queries:
    emb = get_embedding(q)
    vec = "[" + ",".join(str(v) for v in emb) + "]"
    cur.execute(f"""
        SELECT id, text, 1 - (embedding <=> '{vec}'::vector) AS score
        FROM facts
        WHERE thread_id = %s AND superseded_at IS NULL AND embedding IS NOT NULL
        ORDER BY embedding <=> '{vec}'::vector ASC
        LIMIT 25
    """, (THREAD,))
    print(f"\n== TOP-25 thread-scoped for: {q!r} ==")
    for i, (fid, text, score) in enumerate(cur.fetchall()):
        print(f"  [{i:2d}] {round(score, 4):>6}  {text[:90]}")

conn.close()
