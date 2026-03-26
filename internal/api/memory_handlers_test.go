package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmem/internal/account"
	agentsvc "agentmem/internal/agent"
	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/accountrepo"
	"agentmem/internal/repository/agentrepo"
	"agentmem/internal/repository/memoryrepo"
)

func TestContextualHandlerValidation(t *testing.T) {
	server := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/memory/contextual", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFactualHandlerSuccess(t *testing.T) {
	server := newTestServer()
	body := models.FactualInput{
		AgentID:   "",
		SessionID: "sess-1",
		Inputs: []models.InputItem{
			{Kind: models.SOURCE_USER, Content: "Always include tests", ContentType: "text/plain"},
		},
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/memory/factual", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetFactNotFound(t *testing.T) {
	server := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/facts/not-existing", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestProtectedRouteRequiresAPIKey(t *testing.T) {
	server := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/memory/contextual", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func newTestServer() *Server {
	repo := memoryrepo.NewInMemory()
	memEngine := engine.NewMemoryEngine(nil, repo, "", "")
	accountSvc := account.NewService(&mockAccountRepo{})
	agentSvc := agentsvc.NewService(&mockAgentRepo{})
	return NewServer(memEngine, accountSvc, agentSvc)
}

const (
	testAccountID = "11111111-1111-1111-1111-111111111111"
	testAPIKey    = "amk_abcdef123456_00112233445566778899aabbccddeeff0011223344556677"
)

type mockAccountRepo struct{}

func (m *mockAccountRepo) CreateAccount(context.Context, string) (*models.Account, error) {
	return nil, errors.New("unexpected call")
}

func (m *mockAccountRepo) CreateAPIKey(context.Context, accountrepo.CreateAPIKeyParams) (*models.APIKey, error) {
	return nil, errors.New("unexpected call")
}

func (m *mockAccountRepo) InvalidateAPIKeyByID(context.Context, string) (bool, error) {
	return false, errors.New("unexpected call")
}

func (m *mockAccountRepo) InvalidateAPIKeyByPrefix(context.Context, string) (bool, error) {
	return false, errors.New("unexpected call")
}

func (m *mockAccountRepo) ListAPIKeysByPrefix(_ context.Context, prefix string) ([]models.APIKey, error) {
	if prefix != "abcdef123456" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(testAPIKey))
	return []models.APIKey{
		{
			ID:        "22222222-2222-2222-2222-222222222222",
			AccountID: testAccountID,
			Prefix:    prefix,
			KeyHash:   hex.EncodeToString(sum[:]),
			Valid:     true,
			CreatedAt: time.Now().UTC(),
		},
	}, nil
}

type mockAgentRepo struct{}

func (m *mockAgentRepo) CreateAgent(context.Context, string, string) (*models.Agent, error) {
	return nil, errors.New("unexpected call")
}

func (m *mockAgentRepo) GetAgentByID(context.Context, string, string) (*models.Agent, error) {
	return nil, nil
}

func (m *mockAgentRepo) DeleteAgentByID(context.Context, string, string) (bool, error) {
	return false, nil
}

func (m *mockAgentRepo) CreateSession(context.Context, string, string) (*models.Session, error) {
	return nil, errors.New("unexpected call")
}

func (m *mockAgentRepo) GetSessionByID(context.Context, string, string, string) (*models.Session, error) {
	return nil, nil
}

func (m *mockAgentRepo) CloseSessionByID(context.Context, string, string, string) (bool, error) {
	return false, nil
}

var _ agentrepo.Repository = (*mockAgentRepo)(nil)
