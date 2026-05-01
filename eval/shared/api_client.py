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

    def _raise_with_body(self, resp: httpx.Response) -> None:
        try:
            resp.raise_for_status()
        except httpx.HTTPStatusError as e:
            body = resp.text[:500] if resp.text else "<empty>"
            raise httpx.HTTPStatusError(
                f"{e.args[0]} | body: {body}",
                request=e.request,
                response=e.response,
            ) from e

    async def create_thread(self) -> dict:
        """Create a new thread for the configured agent."""
        payload = {"agent_id": self.agent_id}
        resp = await self._client_or_raise().post(f"{self.base_url}/threads", json=payload)
        self._raise_with_body(resp)
        return resp.json()

    async def ingest(
        self,
        thread_id: str,
        role: str,
        content: str,
        author: str | None = None,
        when: str | None = None,
        image_caption: str | None = None,
    ) -> dict:
        """Send a single conversation turn to /memory/contextual for ingestion.

        Args:
            when: ISO 8601 timestamp string for when the message was produced.
                  Sent as event_date; used to resolve relative dates in fact extraction.
            image_caption: Optional description of an image attached to this turn.
                  Inlined into the same InputItem as the text (rather than a
                  separate item) so the decomposer sees text + image as one
                  coherent message in a single LLM call. A separate item gets
                  decomposed in isolation and the BLIP caption — the only place
                  some descriptive attributes appear (e.g. "sunset with a pink
                  sky") — can get dropped.
        """
        kind = _ROLE_TO_KIND.get(role.lower(), role.upper())

        text = content
        if image_caption:
            attribution = author or "speaker"
            text = f"{content}\n\n[{attribution} shared an image: {image_caption}]"

        item: dict = {"kind": kind, "content": text, "content_type": "text/plain"}
        if author:
            item["author"] = author
        if when:
            item["event_date"] = when

        payload = {"thread_id": thread_id, "inputs": [item]}
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/contextual", json=payload)
        self._raise_with_body(resp)
        return resp.json()

    async def recall(self, thread_id: str, question: str, when: str | None = None) -> dict:
        """Read-only retrieval of facts relevant to the question.

        Args:
            when: ISO 8601 timestamp string for when the question is being asked.
                  Sent as event_date; used to resolve relative-time phrases in the query.
        """
        payload = {
            "thread_id": thread_id,
            "agent_id": self.agent_id,
            "query": question,
        }
        if when:
            payload["event_date"] = when
        resp = await self._client_or_raise().post(f"{self.base_url}/memory/recall", json=payload)
        self._raise_with_body(resp)
        return resp.json()


class Mem0APIClient:
    """Thin async wrapper around the mem0 API, with the same interface as MemoryAPIClient."""

    def __init__(self, api_key: str):
        self.base_url = "https://api.mem0.ai/v3"
        self.api_key = api_key
        self._client: httpx.AsyncClient | None = None

    async def __aenter__(self):
        self._client = httpx.AsyncClient(
            timeout=DEFAULT_TIMEOUT,
            headers={
                "Authorization": f"Token {self.api_key}",
                "Content-Type": "application/json",
            },
        )
        return self

    async def __aexit__(self, *_):
        if self._client:
            await self._client.aclose()

    def _client_or_raise(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError("Mem0APIClient must be used as an async context manager")
        return self._client

    def _raise_with_body(self, resp: httpx.Response) -> None:
        try:
            resp.raise_for_status()
        except httpx.HTTPStatusError as e:
            body = resp.text[:500] if resp.text else "<empty>"
            raise httpx.HTTPStatusError(
                f"{e.args[0]} | body: {body}",
                request=e.request,
                response=e.response,
            ) from e

    async def create_thread(self) -> dict:
        import uuid
        return {"id": str(uuid.uuid4())}

    async def ingest(
        self,
        thread_id: str,
        role: str,
        content: str,
        author: str | None = None,
        when: str | None = None,
        image_caption: str | None = None,
    ) -> dict:
        mem0_role = "user" if role.lower() == "user" else "assistant"
        text = content
        if image_caption:
            text += f"\n[image attached: {image_caption}]"
        payload: dict = {
            "messages": [{"role": mem0_role, "content": text}],
            "user_id": thread_id,
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memories/add/", json=payload)
        self._raise_with_body(resp)
        return resp.json()

    async def recall(self, thread_id: str, question: str, when: str | None = None) -> dict:
        payload = {
            "query": question,
            "filters": {"OR": [{"user_id": thread_id}]},
        }
        resp = await self._client_or_raise().post(f"{self.base_url}/memories/search/", json=payload)
        self._raise_with_body(resp)
        data = resp.json()

        facts = []
        items = data if isinstance(data, list) else data.get("results", data.get("memories", []))
        for item in items:
            if isinstance(item, dict) and "memory" in item:
                facts.append({"text": item["memory"]})

        return {"facts": facts, "debug": data}
