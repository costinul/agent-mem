package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func TestContextualHandlerValidation(t *testing.T) {
	server := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/memory/contextual", bytes.NewBufferString(`{"account_id":"a"}`))
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFactualHandlerSuccess(t *testing.T) {
	server := newTestServer()
	body := models.FactualInput{
		AccountID: "acct-1",
		AgentID:   "agent-1",
		SessionID: "sess-1",
		Inputs: []models.InputItem{
			{Kind: models.SOURCE_USER, Content: "Always include tests", ContentType: "text/plain"},
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/memory/factual", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestGetFactNotFound(t *testing.T) {
	server := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/facts/not-existing", nil)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func newTestServer() *Server {
	repo := memoryrepo.NewInMemory()
	memEngine := engine.NewMemoryEngine(nil, repo)
	return NewServer(memEngine)
}
