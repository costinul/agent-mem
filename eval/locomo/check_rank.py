"""
Check where specific facts rank in the vector search for their target queries.
Uses the same embedding model + DB as the running server.
"""
import asyncio
import os
import json
from pathlib import Path
from dotenv import load_dotenv
import psycopg2
import httpx

load_dotenv(Path(__file__).resolve().parent.parent.parent / ".env")

THREAD = '8fa46be9-eb4f-475d-b088-ea3d68c746da'
AGENT  = '640fef54-85e8-4174-a2bb-8822c84b606a'
API    = os.environ.get("MEMORY_API_URL", "http://localhost:8080")
KEY    = os.environ["MEMORY_API_KEY"]

# We'll use a small helper endpoint: embed via the bwai embedding model
# Since we can't call the engine directly, use a python openai client with same model.
from openai import AzureOpenAI

client = AzureOpenAI(
    api_key=os.environ["AZURE_OPENAI_API_KEY"],
    azure_endpoint=os.environ.get("AZURE_OPENAI_ENDPOINT", "https://cchat-ai.cognitiveservices.azure.com/"),
    api_version="2024-02-15-preview",
)

def get_embedding(text: str) -> list[float]:
    resp = client.embeddings.create(model="text-embedding-3-small", input=[text])
    return resp.data[0].embedding


def rank_fact_for_query(conn, query_embedding: list[float], target_text_snippet: str, thread_id: str) -> tuple[int, float, str]:
    """Return (rank, score, text) of the first fact matching target_text_snippet."""
    cur = conn.cursor()
    vec_literal = "[" + ",".join(str(v) for v in query_embedding) + "]"
    cur.execute(f"""
        SELECT id, text, 1 - (embedding <=> '{vec_literal}'::vector) AS score
        FROM facts
        WHERE thread_id = %s AND superseded_at IS NULL AND embedding IS NOT NULL
        ORDER BY embedding <=> '{vec_literal}'::vector ASC
        LIMIT 200
    """, (thread_id,))
    rows = cur.fetchall()
    for rank, (fid, text, score) in enumerate(rows, 1):
        if target_text_snippet.lower() in text.lower():
            return rank, round(score, 4), text
    return -1, 0.0, f"(not found in top {len(rows)})"


def main():
    conn = psycopg2.connect(
        host='agent-mem.postgres.database.azure.com',
        port=5432,
        dbname='agent-mem',
        user='agentmemadm',
        password='6dFAW49oXbNLNU8S3gqGLJKp8FTAAB2w',
        sslmode='require',
    )

    probes = [
        # (query, target_text_snippet)
        ("When did Melanie buy the figurines?",         "figurines yesterday"),
        ("What country is Caroline's grandma from?",    "grandma in Sweden"),
        ("Which classical musicians does Melanie enjoy listening to?", "Bach and Mozart"),
        ("Where did Oliver hide his bone once?",        "slipper"),
        ("What was Melanie's favorite book from her childhood?", "Charlotte"),
        ("What did the posters at the poetry reading say?", "Trans Lives Matter"),
        ("What activity did Caroline used to do with her dad?", "horseback"),
        ("What does Caroline's necklace symbolize?",    "love, faith, and strength"),
        # known-passing as control
        ("What book did Caroline recommend to Melanie?", "Becoming Nicole"),
        ("When did Melanie run a charity race?",         "charity"),
    ]

    print(f"{'Query':<55} {'Rank':>5}  {'Score':>6}  Fact excerpt")
    print("-" * 120)
    for query, target_snippet in probes:
        emb = get_embedding(query)
        rank, score, text = rank_fact_for_query(conn, emb, target_snippet, THREAD)
        verdict = "IN_K" if 0 < rank <= 25 else ("TOP100" if 0 < rank <= 100 else "MISS")
        print(f"{query[:54]:<55} {rank:>5}  {score:>6}  [{verdict}] {text[:60]}")

    conn.close()


if __name__ == "__main__":
    main()
