"""
Thin async wrapper around the agent-mem memory API.
"""
import httpx

DEFAULT_TIMEOUT = 60.0

_ROLE_TO_KIND = {
    "user": "USER",
    "agent": "AGENT",
    "system": "SYSTEM",
    "tool": "TOOL",
    "document": "DOCUMENT",
    "code": "CODE",
}


class MemoryAPIClient:
    def __init__(self, base_url: str, api_key: str, agent_id: str):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.agent_id = agent_id
        self._client: httpx.AsyncClient | None = None

    async def __aenter__(self):
        self._client = httpx.AsyncClient(
            timeout=DEFAULT_TIMEOUT,
            headers={"Authorization": f"Bearer {self.api_key}"},
        )
        return self

    async def __aexit__(self, *_):
        if self._client:
            await self._client.aclose()

    def _client_or_raise(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError("MemoryAPIClient must be used as an async context manager")
        return self._client

    async def create_thread(self) -> dict:
        """Create a new thread for the configured agent."""
        payload = {"agent_id": self.agent_id}
        resp = await self._client_or_raise().post(f"{self.base_url}/threads", json=payload)
        resp.raise_for_status()
        return resp.json()

    async def ingest(self, thread_id: str, role: str, content: str, author: str | None = None) -> dict:
        """Send a single conversation turn to /memory/contextual for ingestion."""
        kind = _ROLE_TO_KIND.get(role.lower(), role.upper())
        item: dict = {"kind": kind, "content": content, "content_type": "text/plain"}
        if author:
            item["author"] = author
        payload = {
            "thread_id": thread_id,
            "inputs": [item],
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/contextual", json=payload)
        resp.raise_for_status()
        return resp.json()

    async def recall(self, thread_id: str, question: str) -> dict:
        """Read-only retrieval of facts relevant to the question."""
        payload = {
            "thread_id": thread_id,
            "agent_id": self.agent_id,
            "query": question,
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/recall", json=payload)
        resp.raise_for_status()
        return resp.json()
