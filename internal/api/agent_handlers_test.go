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

func TestCreateGetAndDeleteThread(t *testing.T) {
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

	createThreadReq := httptest.NewRequest(http.MethodPost, "/threads", bytes.NewBufferString(`{"agent_id":"`+created.ID+`"}`))
	createThreadReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createThreadRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createThreadRec, createThreadReq)
	if createThreadRec.Code != http.StatusCreated {
		t.Fatalf("create thread status = %d, want %d", createThreadRec.Code, http.StatusCreated)
	}

	var thread models.Thread
	if err := json.Unmarshal(createThreadRec.Body.Bytes(), &thread); err != nil {
		t.Fatalf("unmarshal thread response: %v", err)
	}
	if thread.ID == "" {
		t.Fatalf("thread id is empty")
	}

	getThreadReq := httptest.NewRequest(http.MethodGet, "/threads/"+thread.ID, nil)
	getThreadReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	getThreadRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getThreadRec, getThreadReq)
	if getThreadRec.Code != http.StatusOK {
		t.Fatalf("get thread status = %d, want %d", getThreadRec.Code, http.StatusOK)
	}

	deleteThreadReq := httptest.NewRequest(http.MethodDelete, "/threads/"+thread.ID, nil)
	deleteThreadReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	deleteThreadRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(deleteThreadRec, deleteThreadReq)
	if deleteThreadRec.Code != http.StatusOK {
		t.Fatalf("delete thread status = %d, want %d", deleteThreadRec.Code, http.StatusOK)
	}
}

func newAgentTestServer() *Server {
	memEngine := engine.NewMemoryEngine(nil, memoryrepo.NewInMemory(), engine.LLMModels{}, "")
	accountSvc := account.NewService(&mockAccountRepo{})
	agentSvc := agentsvc.NewService(newStatefulAgentRepo())
	return NewServer(memEngine, accountSvc, agentSvc, nil)
}

type statefulAgentRepo struct {
	agents  map[string]models.Agent
	threads map[string]models.Thread
}

func newStatefulAgentRepo() *statefulAgentRepo {
	return &statefulAgentRepo{
		agents:  make(map[string]models.Agent),
		threads: make(map[string]models.Thread),
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

func (r *statefulAgentRepo) CreateThread(_ context.Context, accountID, agentID string) (*models.Thread, error) {
	thread := models.Thread{
		ID:        "55555555-5555-5555-5555-555555555555",
		AccountID: accountID,
		AgentID:   agentID,
		CreatedAt: time.Now().UTC(),
	}
	if len(r.threads) > 0 {
		thread.ID = "66666666-6666-6666-6666-666666666666"
	}
	r.threads[thread.ID] = thread
	copy := thread
	return &copy, nil
}

func (r *statefulAgentRepo) GetThreadByID(_ context.Context, accountID, threadID string) (*models.Thread, error) {
	thread, ok := r.threads[threadID]
	if !ok || thread.AccountID != accountID {
		return nil, nil
	}
	copy := thread
	return &copy, nil
}

func (r *statefulAgentRepo) DeleteThreadByID(_ context.Context, accountID, threadID string) (bool, error) {
	thread, ok := r.threads[threadID]
	if !ok || thread.AccountID != accountID {
		return false, nil
	}
	delete(r.threads, threadID)
	return true, nil
}

func (r *statefulAgentRepo) ListAllAgents(context.Context, string) ([]models.Agent, error) {
	return nil, nil
}

func (r *statefulAgentRepo) UpdateAgent(context.Context, string, string, string) error {
	return nil
}

func (r *statefulAgentRepo) ListAllThreads(context.Context, string, *string) ([]models.Thread, error) {
	return nil, nil
}
