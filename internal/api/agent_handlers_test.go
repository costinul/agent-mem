package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentmem/internal/account"
	agentsvc "agentmem/internal/agent"
	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func TestCreateAndGetAgent(t *testing.T) {
	server := newAgentTestServer()

	createReq := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewBufferString(`{"name":"assistant-1"}`))
	createReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", createRec.Code, http.StatusCreated)
	}

	var created models.Agent
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("create returned empty id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/agents/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	getRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", getRec.Code, http.StatusOK)
	}
}

func TestCreateAndDeleteSession(t *testing.T) {
	server := newAgentTestServer()

	createAgentReq := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewBufferString(`{"name":"assistant-2"}`))
	createAgentReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createAgentRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createAgentRec, createAgentReq)

	var created models.Agent
	_ = json.Unmarshal(createAgentRec.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatalf("agent id is empty")
	}

	createSessionReq := httptest.NewRequest(http.MethodPost, "/agents/"+created.ID+"/sessions", nil)
	createSessionReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createSessionRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createSessionRec, createSessionReq)
	if createSessionRec.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want %d", createSessionRec.Code, http.StatusCreated)
	}

	var session models.Session
	if err := json.Unmarshal(createSessionRec.Body.Bytes(), &session); err != nil {
		t.Fatalf("unmarshal session response: %v", err)
	}
	if session.ID == "" {
		t.Fatalf("session id is empty")
	}

	deleteSessionReq := httptest.NewRequest(http.MethodDelete, "/agents/"+created.ID+"/sessions/"+session.ID, nil)
	deleteSessionReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	deleteSessionRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(deleteSessionRec, deleteSessionReq)
	if deleteSessionRec.Code != http.StatusOK {
		t.Fatalf("delete session status = %d, want %d", deleteSessionRec.Code, http.StatusOK)
	}
}

func newAgentTestServer() *Server {
	memEngine := engine.NewMemoryEngine(nil, memoryrepo.NewInMemory(), "", "")
	accountSvc := account.NewService(&mockAccountRepo{})
	agentSvc := agentsvc.NewService(newStatefulAgentRepo())
	return NewServer(memEngine, accountSvc, agentSvc)
}

type statefulAgentRepo struct {
	agents   map[string]models.Agent
	sessions map[string]models.Session
}

func newStatefulAgentRepo() *statefulAgentRepo {
	return &statefulAgentRepo{
		agents:   make(map[string]models.Agent),
		sessions: make(map[string]models.Session),
	}
}

func (r *statefulAgentRepo) CreateAgent(_ context.Context, accountID, name string) (*models.Agent, error) {
	agent := models.Agent{
		ID:        "33333333-3333-3333-3333-333333333333",
		AccountID: accountID,
		Name:      name,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if len(r.agents) > 0 {
		agent.ID = "44444444-4444-4444-4444-444444444444"
	}
	r.agents[agent.ID] = agent
	copy := agent
	return &copy, nil
}

func (r *statefulAgentRepo) GetAgentByID(_ context.Context, accountID, agentID string) (*models.Agent, error) {
	agent, ok := r.agents[agentID]
	if !ok || agent.AccountID != accountID {
		return nil, nil
	}
	copy := agent
	return &copy, nil
}

func (r *statefulAgentRepo) DeleteAgentByID(_ context.Context, accountID, agentID string) (bool, error) {
	agent, ok := r.agents[agentID]
	if !ok || agent.AccountID != accountID {
		return false, nil
	}
	delete(r.agents, agentID)
	return true, nil
}

func (r *statefulAgentRepo) CreateSession(_ context.Context, accountID, agentID string) (*models.Session, error) {
	session := models.Session{
		ID:        "55555555-5555-5555-5555-555555555555",
		AccountID: accountID,
		AgentID:   agentID,
		CreatedAt: time.Now().UTC(),
	}
	if len(r.sessions) > 0 {
		session.ID = "66666666-6666-6666-6666-666666666666"
	}
	r.sessions[session.ID] = session
	copy := session
	return &copy, nil
}

func (r *statefulAgentRepo) GetSessionByID(_ context.Context, accountID, agentID, sessionID string) (*models.Session, error) {
	session, ok := r.sessions[sessionID]
	if !ok || session.AccountID != accountID || session.AgentID != agentID {
		return nil, nil
	}
	copy := session
	return &copy, nil
}

func (r *statefulAgentRepo) CloseSessionByID(_ context.Context, accountID, agentID, sessionID string) (bool, error) {
	session, ok := r.sessions[sessionID]
	if !ok || session.AccountID != accountID || session.AgentID != agentID {
		return false, nil
	}
	now := time.Now().UTC()
	session.ClosedAt = &now
	r.sessions[sessionID] = session
	return true, nil
}
