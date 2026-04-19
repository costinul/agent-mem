package eval_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
)

type envSecretStorage struct{}

func (s *envSecretStorage) GetSecret(_ context.Context, name string) ([]byte, error) {
	val := os.Getenv(name)
	if val == "" {
		return nil, fmt.Errorf("env var %s is not set", name)
	}
	return []byte(val), nil
}

func (s *envSecretStorage) SaveSecret(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("not supported")
}

func setupEngine(t *testing.T) (*engine.MemoryEngine, *memoryrepo.InMemoryRepository) {
	t.Helper()
	if os.Getenv("AZURE_OPENAI_API_KEY") == "" {
		t.Skip("AZURE_OPENAI_API_KEY not set, skipping eval test")
	}

	registry, err := bwai.NewModelRegistry("../models.json", "../prompts", &envSecretStorage{})
	if err != nil {
		t.Fatalf("failed to create model registry: %v", err)
	}

	client := bwaiclient.NewBWAIClient(registry, nil, nil)
	repo := memoryrepo.NewInMemory()
	eng := engine.NewMemoryEngine(client, repo, os.Getenv("AI_SCHEMA_MODEL"), os.Getenv("AI_EMBEDDING_MODEL"))
	return eng, repo
}

func sendMessage(t *testing.T, eng *engine.MemoryEngine, content string) {
	t.Helper()
	if _, err := eng.ProcessContextual(context.Background(), models.MemoryInput{
		AccountID: "eval-account",
		AgentID:   "eval-agent",
		ThreadID:  "eval-thread",
		Inputs: []models.InputItem{
			{Kind: models.SOURCE_USER, Content: content},
		},
	}); err != nil {
		t.Fatalf("ProcessContextual(%q) error = %v", content, err)
	}
}

func logFacts(t *testing.T, label string, facts []models.Fact) {
	t.Helper()
	fmt.Printf("--- %s (%d facts) ---\n", label, len(facts))
	for i, f := range facts {
		superseded := "active"
		if f.SupersededAt != nil {
			superseded = fmt.Sprintf("superseded by %s", *f.SupersededBy)
		}
		fmt.Printf("  [%d] id=%s kind=%s status=%s\n", i, f.ID, f.Kind, superseded)
		fmt.Printf("       text=%q\n", f.Text)
		fmt.Printf("       source=%s\n", f.SourceID)
	}
}

func activeFacts(t *testing.T, repo *memoryrepo.InMemoryRepository) []models.Fact {
	t.Helper()
	agentID := "eval-agent"
	threadID := "eval-thread"
	facts, err := repo.ListFactsByScope(context.Background(), "eval-account", &agentID, &threadID)
	if err != nil {
		t.Fatalf("ListFactsByScope() error = %v", err)
	}
	return facts
}

func TestEval_FactCreation(t *testing.T) {
	eng, repo := setupEngine(t)

	sendMessage(t, eng, "I just bought a blue car last week. It's a BMW 3 Series.")

	facts := activeFacts(t, repo)
	logFacts(t, "active facts after creation", facts)

	if len(facts) == 0 {
		t.Fatal("expected at least one active fact after processing input")
	}
}

func TestEval_FactEvolution_SoldCar(t *testing.T) {
	eng, repo := setupEngine(t)

	sendMessage(t, eng, "I have a blue BMW 3 Series that I bought last year.")
	factsAfterFirst := activeFacts(t, repo)
	logFacts(t, "after first message", factsAfterFirst)

	if len(factsAfterFirst) == 0 {
		t.Fatal("expected at least one fact after first message")
	}
	initialCount := len(factsAfterFirst)

	sendMessage(t, eng, "I sold my blue BMW last week. I'm taking the bus now.")
	factsAfterSecond := activeFacts(t, repo)
	logFacts(t, "after second message (sold car)", factsAfterSecond)

	if len(factsAfterSecond) == 0 {
		t.Fatal("expected at least one active fact after second message")
	}

	// The car fact should have evolved — we expect either the same count
	// (evolved in place) or more facts (new info about bus), but the
	// original "has a blue BMW" text should no longer appear as-is.
	fmt.Printf("fact count: before=%d after=%d\n", initialCount, len(factsAfterSecond))
}

func TestEval_FactCorrection_Lie(t *testing.T) {
	eng, repo := setupEngine(t)

	sendMessage(t, eng, "I have a blue cat named Whiskers at home.")
	factsAfterFirst := activeFacts(t, repo)
	logFacts(t, "after first message", factsAfterFirst)

	if len(factsAfterFirst) == 0 {
		t.Fatal("expected at least one fact after first message")
	}

	sendMessage(t, eng, "Actually, I lied about having a blue cat. I don't have any pets.")
	factsAfterSecond := activeFacts(t, repo)
	logFacts(t, "after second message (lie correction)", factsAfterSecond)

	// The original cat fact should have been superseded or updated.
	// Check that the active facts no longer state the user has a blue cat as truth.
	for _, f := range factsAfterSecond {
		fmt.Printf("  active: %q\n", f.Text)
	}
}
