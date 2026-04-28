package engine

import (
	"math"
	"testing"
	"time"

	models "agentmem/internal/models"
)

// ── dateRerank ────────────────────────────────────────────────────

func TestDateRerank_NilRefAtIsEligible(t *testing.T) {
	eventDate := mustParseDate("2023-10-22")
	f := makeFactWithRef("f1", nil)
	got, eligible, future := dateRerank([]models.Fact{f}, eventDate)
	if len(got) != 1 || got[0].ID != "f1" {
		t.Fatalf("got %v, want [f1]", factIDSlice(got))
	}
	if eligible != 1 || future != 0 {
		t.Fatalf("eligible=%d future=%d, want 1,0", eligible, future)
	}
}

func TestDateRerank_PastDateIsEligible(t *testing.T) {
	eventDate := mustParseDate("2023-10-22")
	ref := mustParseDate("2023-05-07")
	f := makeFactWithRef("f1", &ref)
	_, eligible, future := dateRerank([]models.Fact{f}, eventDate)
	if eligible != 1 || future != 0 {
		t.Fatalf("eligible=%d future=%d, want 1,0", eligible, future)
	}
}

func TestDateRerank_SameDayIsEligible(t *testing.T) {
	eventDate := mustParseDate("2023-10-22")
	ref := mustParseDate("2023-10-22")
	f := makeFactWithRef("f1", &ref)
	_, eligible, future := dateRerank([]models.Fact{f}, eventDate)
	if eligible != 1 || future != 0 {
		t.Fatalf("eligible=%d future=%d, want 1,0", eligible, future)
	}
}

func TestDateRerank_FutureDateIsDemoted(t *testing.T) {
	eventDate := mustParseDate("2023-05-08")
	futureRef := mustParseDate("2023-07-10")
	fFuture := makeFactWithRef("future", &futureRef)
	fTimeless := makeFactWithRef("timeless", nil)

	got, eligible, future := dateRerank([]models.Fact{fFuture, fTimeless}, eventDate)
	if got[0].ID != "timeless" || got[1].ID != "future" {
		t.Fatalf("expected [timeless future], got %v", factIDSlice(got))
	}
	if eligible != 1 || future != 1 {
		t.Fatalf("eligible=%d future=%d, want 1,1", eligible, future)
	}
}

func TestDateRerank_OldPastFactLeadsFutureOne(t *testing.T) {
	// A historically-dated fact (May 2023) should stay ahead of a future one.
	eventDate := mustParseDate("2023-10-22")
	pastRef := mustParseDate("2023-05-07")
	futureRef := mustParseDate("2024-01-01")
	fPast := makeFactWithRef("past", &pastRef)
	fFuture := makeFactWithRef("future", &futureRef)

	got, eligible, futureCount := dateRerank([]models.Fact{fPast, fFuture}, eventDate)
	if got[0].ID != "past" || got[1].ID != "future" {
		t.Fatalf("expected [past future], got %v", factIDSlice(got))
	}
	if eligible != 1 || futureCount != 1 {
		t.Fatalf("eligible=%d future=%d, want 1,1", eligible, futureCount)
	}
}

func TestDateRerank_EmptyInput(t *testing.T) {
	eventDate := mustParseDate("2023-10-22")
	got, eligible, future := dateRerank(nil, eventDate)
	if len(got) != 0 || eligible != 0 || future != 0 {
		t.Fatalf("expected empty result, got len=%d eligible=%d future=%d", len(got), eligible, future)
	}
}

// ── cosineRerank ──────────────────────────────────────────────────

func TestCosineRerank_HigherSimFirst(t *testing.T) {
	query := vec(1, 0, 0) // unit vector along dim 0
	// fact "close" has high sim to query; "far" is orthogonal.
	close := makeFactWithEmb("close", vec(0.9, 0.1, 0))
	far := makeFactWithEmb("far", vec(0, 1, 0))

	got := cosineRerank([]models.Fact{far, close}, [][]float64{query})
	if got[0].ID != "close" {
		t.Fatalf("expected close first, got %q", got[0].ID)
	}
}

func TestCosineRerank_EmptyEmbeddingSinkToEnd(t *testing.T) {
	query := vec(1, 0, 0)
	hasEmb := makeFactWithEmb("has-emb", vec(0.8, 0.2, 0))
	noEmb := makeFactWithEmb("no-emb", nil)

	got := cosineRerank([]models.Fact{noEmb, hasEmb}, [][]float64{query})
	if got[0].ID != "has-emb" {
		t.Fatalf("expected has-emb first, got %q", got[0].ID)
	}
}

func TestCosineRerank_MultipleQueryPhrases_MaxWins(t *testing.T) {
	// fact A is close to phrase 1; fact B is close to phrase 2.
	// Both should beat fact C which is far from both.
	phrase1 := vec(1, 0, 0)
	phrase2 := vec(0, 1, 0)

	factA := makeFactWithEmb("a", vec(0.9, 0.1, 0)) // close to phrase1
	factB := makeFactWithEmb("b", vec(0.1, 0.9, 0)) // close to phrase2
	factC := makeFactWithEmb("c", vec(0, 0, 1))     // orthogonal to both

	got := cosineRerank([]models.Fact{factC, factC, factA, factB}, [][]float64{phrase1, phrase2})
	// A and B should both appear before either C.
	ids := factIDSlice(got)
	if ids[len(ids)-1] != "c" && ids[len(ids)-2] != "c" {
		t.Fatalf("expected c facts last, got %v", ids)
	}
}

func TestCosineRerank_EmptyInputsReturnEmpty(t *testing.T) {
	if got := cosineRerank(nil, [][]float64{vec(1, 0)}); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
	if got := cosineRerank([]models.Fact{{ID: "x"}}, nil); len(got) != 1 {
		t.Fatalf("expected passthrough, got %v", got)
	}
}

func TestCosineRerank_StableOrderOnTie(t *testing.T) {
	// Two facts with identical embeddings should preserve original order.
	query := vec(1, 0, 0)
	f1 := makeFactWithEmb("first", vec(1, 0, 0))
	f2 := makeFactWithEmb("second", vec(1, 0, 0))

	got := cosineRerank([]models.Fact{f1, f2}, [][]float64{query})
	if got[0].ID != "first" || got[1].ID != "second" {
		t.Fatalf("expected stable order [first second], got %v", factIDSlice(got))
	}
}

// ── l2Norm / maxCosine unit checks ───────────────────────────────

func TestL2Norm(t *testing.T) {
	if got := l2Norm(vec(3, 4)); math.Abs(got-5) > 1e-9 {
		t.Fatalf("l2Norm([3,4]) = %v, want 5", got)
	}
}

func TestMaxCosine_OrthogonalIsZero(t *testing.T) {
	if got := maxCosine(vec(1, 0), [][]float64{vec(0, 1)}); got > 1e-9 {
		t.Fatalf("expected 0 for orthogonal vectors, got %v", got)
	}
}

func TestMaxCosine_ParallelIsOne(t *testing.T) {
	if got := maxCosine(vec(2, 0), [][]float64{vec(5, 0)}); math.Abs(got-1) > 1e-9 {
		t.Fatalf("expected 1 for parallel vectors, got %v", got)
	}
}

// ── normalizeFactText ─────────────────────────────────────────────

func TestNormalizeFactText_StripsAsMentionedOn(t *testing.T) {
	in := "Melanie ran a charity race for mental health on Saturday, 20 May 2023 (as mentioned on 2023-05-25)"
	want := "melanie ran a charity race for mental health on saturday, 20 may 2023"
	if got := normalizeFactText(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeFactText_StripsOriginallySaid(t *testing.T) {
	in := "Melanie took her family camping in the mountains in the week before 27 June 2023 (originally said 'last week' on 27 June 2023)."
	want := "melanie took her family camping in the mountains in the week before 27 june 2023"
	if got := normalizeFactText(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeFactText_StripsAsOf(t *testing.T) {
	in := "Caroline lives in Portland (as of 2023-05-25)"
	want := "caroline lives in portland"
	if got := normalizeFactText(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeFactText_VerbatimAndProvenanceCompareEqual(t *testing.T) {
	a := "Melanie went camping with her family two weekends before Monday, 17 July 2023."
	b := "Melanie went camping with her family two weekends before Monday, 17 July 2023. (as mentioned on 2023-07-17)"
	if normalizeFactText(a) != normalizeFactText(b) {
		t.Fatalf("expected equal normalizations, got %q vs %q", normalizeFactText(a), normalizeFactText(b))
	}
}

func TestNormalizeFactText_CollapsesWhitespace(t *testing.T) {
	in := "  Melanie  loves    camping  "
	want := "melanie loves camping"
	if got := normalizeFactText(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeFactText_PreservesNonProvenanceParenthetical(t *testing.T) {
	in := "Melanie went camping (with her kids) in June 2023"
	want := "melanie went camping (with her kids) in june 2023"
	if got := normalizeFactText(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeFactText_DifferentSubjectsStayDifferent(t *testing.T) {
	a := "Melanie always looks forward to her family camping trip"
	b := "Melanie and her family always look forward to their family camping trip"
	if normalizeFactText(a) == normalizeFactText(b) {
		t.Fatalf("expected different normalizations to remain different, both became %q", normalizeFactText(a))
	}
}

// ── dedupByText ───────────────────────────────────────────────────

func TestDedupByText_DropsVerbatimDuplicate(t *testing.T) {
	a := models.Fact{ID: "a", Text: "Melanie took her family camping (originally said 'last week' on 27 June 2023)"}
	b := models.Fact{ID: "b", Text: "Melanie took her family camping (originally said 'last week' on 27 June 2023)"}
	c := models.Fact{ID: "c", Text: "Melanie took her family camping (originally said 'last week' on 27 June 2023)"}
	got := dedupByText([]models.Fact{a, b, c})
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected only a kept, got %v", factIDSlice(got))
	}
}

func TestDedupByText_KeepsFirstOccurrence(t *testing.T) {
	high := models.Fact{ID: "high-score", Text: "Melanie went camping in June 2023"}
	low := models.Fact{ID: "low-score", Text: "Melanie went camping in June 2023 (as mentioned on 2023-06-30)"}
	got := dedupByText([]models.Fact{high, low})
	if len(got) != 1 || got[0].ID != "high-score" {
		t.Fatalf("expected first occurrence kept, got %v", factIDSlice(got))
	}
}

func TestDedupByText_PreservesDistinctFacts(t *testing.T) {
	a := models.Fact{ID: "a", Text: "Melanie went camping in June 2023"}
	b := models.Fact{ID: "b", Text: "Melanie went camping in July 2023"}
	got := dedupByText([]models.Fact{a, b})
	if len(got) != 2 {
		t.Fatalf("expected both kept, got %v", factIDSlice(got))
	}
}

func TestDedupByText_EmptyInput(t *testing.T) {
	if got := dedupByText(nil); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestDedupByText_SingleFactPassesThrough(t *testing.T) {
	f := models.Fact{ID: "only", Text: "Melanie went camping"}
	got := dedupByText([]models.Fact{f})
	if len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("expected single fact kept, got %v", factIDSlice(got))
	}
}

func TestDedupByText_EmptyTextNotCoalesced(t *testing.T) {
	a := models.Fact{ID: "a", Text: ""}
	b := models.Fact{ID: "b", Text: ""}
	got := dedupByText([]models.Fact{a, b})
	if len(got) != 2 {
		t.Fatalf("empty-text facts must not be coalesced, got %v", factIDSlice(got))
	}
}

// ── helpers ───────────────────────────────────────────────────────

func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func makeFactWithRef(id string, referencedAt *time.Time) models.Fact {
	return models.Fact{ID: id, Text: "fact " + id, ReferencedAt: referencedAt}
}

func makeFactWithEmb(id string, embedding []float64) models.Fact {
	return models.Fact{ID: id, Text: "fact " + id, Embedding: embedding}
}

// vec constructs a float64 slice for use as a test embedding.
func vec(vals ...float64) []float64 { return vals }

func factIDSlice(facts []models.Fact) []string {
	ids := make([]string, len(facts))
	for i, f := range facts {
		ids[i] = f.ID
	}
	return ids
}
