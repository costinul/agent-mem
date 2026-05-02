package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentsvc "agentmem/internal/agent"
	"agentmem/internal/engine"
	"agentmem/internal/repository/agentrepo"
	"agentmem/internal/repository/memoryrepo"

	"agentmem/internal/account"
)

// recallSecurityServer builds a server with a real in-memory agentrepo so that
// agent/thread ownership can be exercised without a database.
func recallSecurityServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	repo := agentrepo.NewInMemory()
	svc := agentsvc.NewService(repo)

	// Account A — owner of the API key used in requests.
	agentA, err := repo.CreateAgent(context.Background(), testAccountID, "agent-a")
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	threadA, err := repo.CreateThread(context.Background(), testAccountID, agentA.ID)
	if err != nil {
		t.Fatalf("create thread A: %v", err)
	}

	// Account B — a different account; its IDs must not be accessible via account A's key.
	const otherAccountID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	agentB, err := repo.CreateAgent(context.Background(), otherAccountID, "agent-b")
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}
	threadB, err := repo.CreateThread(context.Background(), otherAccountID, agentB.ID)
	if err != nil {
		t.Fatalf("create thread B: %v", err)
	}

	memEngine := engine.NewMemoryEngine(nil, memoryrepo.NewInMemory(), engine.LLMModels{}, "")
	accountSvc := account.NewService(&mockAccountRepo{})
	server := NewServer(memEngine, accountSvc, svc, nil)

	// Return agent B and thread B IDs — these belong to the other account and
	// should be rejected when requested with account A's API key.
	_ = agentA
	_ = threadA
	return server, agentB.ID, threadB.ID
}

func TestRecallRejectsForeignAgentID(t *testing.T) {
	server, foreignAgentID, _ := recallSecurityServer(t)

	body, _ := json.Marshal(map[string]any{
		"agent_id": foreignAgentID,
		"query":    "anything",
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/recall", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (foreign agent_id must be rejected)", rec.Code, http.StatusNotFound)
	}
}

func TestRecallRejectsForeignThreadID(t *testing.T) {
	server, _, foreignThreadID := recallSecurityServer(t)

	body, _ := json.Marshal(map[string]any{
		"thread_id": foreignThreadID,
		"query":     "anything",
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/recall", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (foreign thread_id must be rejected)", rec.Code, http.StatusNotFound)
	}
}
