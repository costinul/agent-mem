package engine

import (
	"strings"
	"testing"
)

func TestChunkContent_ShortTextReturnedAsIs(t *testing.T) {
	chunks := chunkContent("hello world", 4000, 400)
	if len(chunks) != 1 || chunks[0] != "hello world" {
		t.Fatalf("expected single unchanged chunk, got %v", chunks)
	}
}

func TestChunkContent_EmptyInput(t *testing.T) {
	chunks := chunkContent("", 4000, 400)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for empty input, got %d", len(chunks))
	}
}

func TestChunkContent_ZeroMaxTokensNoChunking(t *testing.T) {
	long := strings.Repeat("word ", 5000)
	chunks := chunkContent(long, 0, 0)
	if len(chunks) != 1 {
		t.Fatalf("maxTokens=0 should return single chunk, got %d chunks", len(chunks))
	}
}

func TestChunkContent_ParagraphBoundaryPreserved(t *testing.T) {
	// Two paragraphs that together exceed maxTokens but each fits individually.
	para1 := strings.Repeat("alpha ", 300)   // ~300 tokens
	para2 := strings.Repeat("beta ", 300)    // ~300 tokens
	text := para1 + "\n\n" + para2
	chunks := chunkContent(text, 400, 0)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks for two large paragraphs, got %d", len(chunks))
	}
	// Each chunk must not exceed maxTokens.
	for i, c := range chunks {
		n := countTokens(c)
		if n > 400 {
			t.Errorf("chunk[%d] has %d tokens, want <= 400", i, n)
		}
	}
}

func TestChunkContent_OverlapPrependedToNextChunk(t *testing.T) {
	para1 := strings.Repeat("one ", 300)
	para2 := strings.Repeat("two ", 300)
	text := para1 + "\n\n" + para2
	chunks := chunkContent(text, 400, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// The second chunk should contain tokens from the first chunk (overlap).
	// We verify that it is longer than para2 alone.
	para2Trimmed := strings.TrimSpace(para2)
	if len(chunks[1]) <= len(para2Trimmed) {
		t.Errorf("chunk[1] len=%d should be > para2 len=%d (overlap expected)", len(chunks[1]), len(para2Trimmed))
	}
}

func TestChunkContent_HardCutForSingleLargeParagraph(t *testing.T) {
	// Single paragraph, no newlines — forces hard token cut.
	text := strings.Repeat("x ", 2000) // ~2000 tokens
	chunks := chunkContent(text, 500, 0)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple hard-cut chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		n := countTokens(c)
		if n > 500 {
			t.Errorf("chunk[%d] has %d tokens, want <= 500", i, n)
		}
	}
}

func TestChunkContent_NoOverlapWhenOnlyOneChunk(t *testing.T) {
	text := "short text"
	chunks := chunkContent(text, 4000, 400)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestCountTokens_BasicSanity(t *testing.T) {
	n := countTokens("hello world")
	if n < 1 || n > 10 {
		t.Errorf("unexpected token count %d for 'hello world'", n)
	}
}

func TestLastNTokensAsText_ReturnsSuffix(t *testing.T) {
	text := strings.Repeat("word ", 50)
	suffix := lastNTokensAsText(text, 10)
	if suffix == "" {
		t.Fatal("expected non-empty suffix")
	}
	if countTokens(suffix) > 10 {
		t.Errorf("suffix has %d tokens, want <= 10", countTokens(suffix))
	}
}

func TestLastNTokensAsText_NGreaterThanLength(t *testing.T) {
	text := "short"
	suffix := lastNTokensAsText(text, 1000)
	if suffix != text {
		t.Errorf("when n >= total tokens, expected full string, got %q", suffix)
	}
}
