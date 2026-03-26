"""
LLM-as-judge evaluator using gpt-4o-mini.

The judge only reads three short things:
  1. The question (from the dataset)
  2. The ground truth answer (from the dataset)
  3. The facts returned by the memory API

It scores whether the returned facts contain enough information to
correctly answer the question.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

from openai import AsyncOpenAI

Score = Literal["pass", "partial", "fail"]

_SYSTEM_PROMPT = """\
You are an evaluator for a memory retrieval system.
Given a question, the expected correct answer, and a list of facts retrieved \
from a memory system, decide whether the retrieved facts contain sufficient \
information to correctly answer the question.

Respond with a JSON object with exactly two fields:
  "score": one of "pass", "partial", or "fail"
  "reason": one short sentence explaining the score

Scoring rules:
  pass    — the facts clearly and fully support the correct answer
  partial — the facts contain related information but are incomplete or imprecise
  fail    — the facts are missing, irrelevant, or contradict the correct answer
"""

_USER_TEMPLATE = """\
Question: {question}
Expected answer: {ground_truth}
Retrieved facts:
{facts}
"""


@dataclass
class JudgeResult:
    score: Score
    reason: str


class Evaluator:
    def __init__(self, api_key: str, model: str = "gpt-4o-mini"):
        self._client = AsyncOpenAI(api_key=api_key)
        self.model = model

    async def judge(self, question: str, ground_truth: str, facts: list[str]) -> JudgeResult:
        facts_text = "\n".join(f"- {f}" for f in facts) if facts else "(no facts returned)"
        user_msg = _USER_TEMPLATE.format(
            question=question,
            ground_truth=ground_truth,
            facts=facts_text,
        )
        response = await self._client.chat.completions.create(
            model=self.model,
            temperature=0.0,
            response_format={"type": "json_object"},
            messages=[
                {"role": "system", "content": _SYSTEM_PROMPT},
                {"role": "user", "content": user_msg},
            ],
        )
        import json
        data = json.loads(response.choices[0].message.content)
        return JudgeResult(score=data.get("score", "fail"), reason=data.get("reason", ""))
