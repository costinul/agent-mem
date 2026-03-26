"""
Thin async wrapper around the agent-mem memory API.
"""
import httpx

DEFAULT_TIMEOUT = 60.0


class MemoryAPIClient:
    def __init__(self, base_url: str, account_id: str, agent_id: str):
        self.base_url = base_url.rstrip("/")
        self.account_id = account_id
        self.agent_id = agent_id
        self._client: httpx.AsyncClient | None = None

    async def __aenter__(self):
        self._client = httpx.AsyncClient(timeout=DEFAULT_TIMEOUT)
        return self

    async def __aexit__(self, *_):
        if self._client:
            await self._client.aclose()

    def _client_or_raise(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError("MemoryAPIClient must be used as an async context manager")
        return self._client

    async def ingest(self, session_id: str, role: str, content: str) -> dict:
        """Send a single conversation turn to /memory/contextual for ingestion."""
        payload = {
            "account_id": self.account_id,
            "agent_id": self.agent_id,
            "session_id": session_id,
            "message_history": 0,
            "inputs": [{"kind": role, "content": content, "content_type": "text/plain"}],
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/contextual", json=payload)
        resp.raise_for_status()
        return resp.json()

    async def query(self, session_id: str, question: str) -> dict:
        """Query memory with a question and return the response (facts + messages)."""
        payload = {
            "account_id": self.account_id,
            "agent_id": self.agent_id,
            "session_id": session_id,
            "message_history": 0,
            "inputs": [{"kind": "user", "content": question, "content_type": "text/plain"}],
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/contextual", json=payload)
        resp.raise_for_status()
        return resp.json()
