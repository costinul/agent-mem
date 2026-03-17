package engine

import "testing"

func TestParseDecompositionJSONRejectsEmptyFact(t *testing.T) {
	_, err := ParseDecompositionJSON(`{"facts":[{"text":"","kind":"KNOWLEDGE"}]}`)
	if err == nil {
		t.Fatalf("expected parse error for empty fact text")
	}
}

func TestParseEvaluateJSONParsesPayload(t *testing.T) {
	parsed, err := ParseEvaluateJSON(`{"facts_to_delete":["f1"]}`)
	if err != nil {
		t.Fatalf("ParseEvaluateJSON() error = %v", err)
	}
	if len(parsed.FactsToDelete) != 1 || parsed.FactsToDelete[0] != "f1" {
		t.Fatalf("unexpected parse result: %+v", parsed)
	}
}
