"""Check DB for null embeddings and probe specific MISS_C facts."""
import os
from pathlib import Path
from dotenv import load_dotenv
load_dotenv(Path(__file__).resolve().parent.parent.parent / ".env")
import psycopg2

conn = psycopg2.connect(os.environ["POSTGRES_DSN"])
cur = conn.cursor()
THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'

cur.execute("""
    SELECT
        COUNT(*) AS total,
        COUNT(embedding) AS with_emb,
        COUNT(*) - COUNT(embedding) AS null_emb
    FROM facts
    WHERE thread_id = %s AND superseded_at IS NULL
""", (THREAD,))
row = cur.fetchone()
total, with_emb, null_emb = row
print(f"Total facts: {total}")
print(f"  with embedding: {with_emb}")
print(f"  NULL embedding: {null_emb}")

# Show texts of facts with null embeddings
if null_emb > 0:
    print("\nFacts with NULL embedding:")
    cur.execute("""
        SELECT id, text
        FROM facts
        WHERE thread_id = %s AND superseded_at IS NULL AND embedding IS NULL
        ORDER BY created_at
    """, (THREAD,))
    for fid, text in cur.fetchall():
        print(f"  [{fid[:8]}] {text[:100]}")

# Probe specific MISS_C texts
print("\nProbing specific MISS_C fact embeddings:")
for snippet in ['Bach and Mozart', 'figurines yesterday', 'grandma in Sweden']:
    cur.execute("""
        SELECT id, text, embedding IS NOT NULL AS has_emb
        FROM facts
        WHERE thread_id = %s AND superseded_at IS NULL AND text ILIKE %s
    """, (THREAD, f'%{snippet}%'))
    rows = cur.fetchall()
    if rows:
        for fid, text, has_emb in rows:
            print(f"  [{fid[:8]}] has_emb={has_emb}  text={text[:80]}")
    else:
        print(f"  '{snippet}': not found in facts table")

conn.close()
