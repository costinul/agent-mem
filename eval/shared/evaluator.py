"""
LLM-as-judge evaluator with strict per-fact relevance + answer-quality scoring.

For every QA pair the judge produces four signals:

    recall_score    — pass / partial / fail
                      Do the returned facts collectively contain enough
                      information to derive the ground-truth answer? (Ignores
                      noise — pure information sufficiency.)

    answer_score    — pass / partial / fail
                      If an answerer used ONLY these facts (no outside
                      knowledge) to write a single answer, how close would it
                      be to the ground truth? Penalizes both missing detail
                      AND distracting/contradictory noise that would mislead
                      the writer.

    fact_relevance  — list[bool], one entry per returned fact
                      Per-fact relevance bitmap. Drives precision, F1, and
                      first-relevant-rank / MRR aggregates downstream.

    reason          — one short sentence rationale.

Why two scores?
    recall_score answers "is the right answer in here at all?". This is what
    the old judge measured and is what makes a noise-dumping retriever look
    great — every extra fact is a free chance to contain the answer.
    answer_score answers "would a downstream consumer actually be able to
    produce the right answer from this list?". A retriever that returns
    30 mostly-irrelevant facts will keep its recall_score but lose
    answer_score the moment the noise becomes confusing.

Derived metrics computed downstream from these primitives:
    precision = sum(fact_relevance) / len(fact_relevance)
    f1        = harmonic mean of precision and recall_score (numeric: pass=1, partial=0.5, fail=0)
    rank      = position (1-indexed) of the first relevant fact
    MRR       = mean of 1/rank
"""
from __future__ import annotations

import json
import logging
from dataclasses import dataclass, field
from typing import Any, Literal

from openai import AsyncAzureOpenAI

logger = logging.getLogger(__name__)

Score = Literal["pass", "partial", "fail"]

SCORE_TO_NUMERIC: dict[str, float] = {"pass": 1.0, "partial": 0.5, "fail": 0.0}

_SYSTEM_PROMPT = """\
You are a strict evaluator for a memory-retrieval system.

You receive:
  - a QUESTION
  - the GROUND-TRUTH answer
  - a numbered list of FACTS retrieved from memory (may be empty)

Return a single JSON object with EXACTLY these fields:

{
  "fact_relevance": [true|false, ...],
  "recall_score":  "pass" | "partial" | "fail",
  "answer_score":  "pass" | "partial" | "fail",
  "reason":        "one short sentence"
}

Definitions:

fact_relevance[i] (0-indexed; entry i corresponds to fact i+1 in the input):
  true  - this single fact carries information that contributes to answering
          the question: a piece of the answer, supporting evidence, anchoring
          context, or counter-evidence for an inference. Be GENEROUS — a fact
          that is only weakly related still counts as relevant if an answerer
          could realistically use it.
  false - this fact is off-topic; an answerer would discard or ignore it.

recall_score (information sufficiency, ignoring noise):
  pass    - the facts collectively contain ALL the information needed to
            derive the ground-truth answer.
  partial - the facts contain SOME of the information needed; key details are
            missing or imprecise.
  fail    - the information needed is not present; the answer cannot be
            derived from these facts.

answer_score (production quality, accounting for noise):
  Imagine an answerer that synthesizes a single answer from these facts only,
  with no outside knowledge.
  pass    - the facts unambiguously support the ground truth; the answer
            would match it.
  partial - the answer would be incomplete, hedged, or include minor errors
            caused by missing detail OR by distracting / contradictory facts
            mixed in.
  fail    - the answerer would produce a wrong answer, refuse, or say
            "I don't know".

Hard rules:
  - fact_relevance MUST have exactly the same length as the input facts list.
    If the facts list is empty, return [].
  - Output ONLY the JSON object. No markdown, no prose, no commentary.
"""

_USER_TEMPLATE = """\
Question: {question}
Ground-truth answer: {ground_truth}

Facts ({n} total):
{facts}
"""


@dataclass
class JudgeResult:
    recall_score: Score
    answer_score: Score
    fact_relevance: list[bool] = field(default_factory=list)
    reason: str = ""

    @property
    def precision(self) -> float | None:
        if not self.fact_relevance:
            return None
        return sum(1 for r in self.fact_relevance if r) / len(self.fact_relevance)

    @property
    def first_relevant_rank(self) -> int | None:
        for i, r in enumerate(self.fact_relevance, 1):
            if r:
                return i
        return None

    @property
    def f1(self) -> float | None:
        # F1 of precision and recall_score (numeric). Undefined when no facts
        # were returned, since precision is undefined.
        p = self.precision
        if p is None:
            return None
        r = SCORE_TO_NUMERIC[self.recall_score]
        if p + r == 0:
            return 0.0
        return 2 * p * r / (p + r)

    def to_dict(self) -> dict[str, Any]:
        return {
            "recall_score": self.recall_score,
            "answer_score": self.answer_score,
            "fact_relevance": self.fact_relevance,
            "precision": self.precision,
            "f1": self.f1,
            "first_relevant_rank": self.first_relevant_rank,
            "reason": self.reason,
        }


class Evaluator:
    def __init__(self, api_key: str, endpoint: str, model: str):
        self._client = AsyncAzureOpenAI(
            api_key=api_key,
            azure_endpoint=endpoint,
            api_version="2024-02-15-preview",
        )
        self.model = model

    async def judge(self, question: str, ground_truth: str, facts: list[str]) -> JudgeResult:
        n = len(facts)
        if facts:
            facts_text = "\n".join(f"{i + 1}. {f}" for i, f in enumerate(facts))
        else:
            facts_text = "(no facts returned)"
        user_msg = _USER_TEMPLATE.format(
            question=question,
            ground_truth=ground_truth,
            n=n,
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
        data = json.loads(response.choices[0].message.content)
        return _parse_result(data, expected_facts=n)


def _parse_result(data: dict, expected_facts: int) -> JudgeResult:
    recall = _coerce_score(data.get("recall_score"))
    answer = _coerce_score(data.get("answer_score"))
    raw_rel = data.get("fact_relevance")
    if not isinstance(raw_rel, list):
        raw_rel = []
    rel = [bool(x) for x in raw_rel]
    # Length-mismatch repair: pad with False (conservative) or truncate. We
    # keep the question scorable rather than failing the whole run, but log
    # so the user can investigate prompt drift.
    if len(rel) != expected_facts:
        logger.warning(
            "judge fact_relevance length mismatch: got %d, expected %d (padding/truncating)",
            len(rel),
            expected_facts,
        )
        if len(rel) < expected_facts:
            rel = rel + [False] * (expected_facts - len(rel))
        else:
            rel = rel[:expected_facts]
    reason = str(data.get("reason", "")).strip()
    return JudgeResult(
        recall_score=recall,
        answer_score=answer,
        fact_relevance=rel,
        reason=reason,
    )


def _coerce_score(value: Any) -> Score:
    if isinstance(value, str) and value.lower() in ("pass", "partial", "fail"):
        return value.lower()  # type: ignore[return-value]
    return "fail"
