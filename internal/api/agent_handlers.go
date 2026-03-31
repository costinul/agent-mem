package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentmem/internal/agent"
	models "agentmem/internal/models"
)

// createAgentHandler creates an agent for the authenticated account.
// @Summary Create Agent
// @Description Create a new agent under the authenticated account.
// @Tags agents
// @Accept json
// @Produce json
// @Param body body memory.AgentCreateBody true "Create Agent Body"
// @Success 201 {object} memory.Agent
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents [post]
func createAgentHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}

		var body models.AgentCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}

		created, err := agentSvc.CreateAgent(r.Context(), accountID, body.Name)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// getAgentHandler retrieves one agent from the authenticated account.
// @Summary Get Agent
// @Description Retrieve one agent by ID under the authenticated account.
// @Tags agents
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} memory.Agent
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents/{id} [get]
func getAgentHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		agentID := strings.TrimSpace(r.PathValue("id"))
		if agentID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id is required"})
			return
		}

		found, err := agentSvc.GetAgent(r.Context(), accountID, agentID)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}

// deleteAgentHandler deletes one agent from the authenticated account.
// @Summary Delete Agent
// @Description Delete one agent and all related records under the authenticated account.
// @Tags agents
// @Produce json
// @Param id path string true "Agent ID"
// @Success 200 {object} map[string]string "{"status":"deleted"}"
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents/{id} [delete]
func deleteAgentHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		agentID := strings.TrimSpace(r.PathValue("id"))
		if agentID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id is required"})
			return
		}

		if err := agentSvc.DeleteAgent(r.Context(), accountID, agentID); err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// createThreadHandler creates a thread for an agent in the authenticated account.
// @Summary Create Thread
// @Description Create a new thread for an agent under the authenticated account.
// @Tags threads
// @Accept json
// @Produce json
// @Param body body memory.ThreadCreateBody true "Create Thread Body"
// @Success 201 {object} memory.Thread
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /threads [post]
func createThreadHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}

		var body models.ThreadCreateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}
		agentID := strings.TrimSpace(body.AgentID)
		if agentID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id is required"})
			return
		}

		thread, err := agentSvc.CreateThread(r.Context(), accountID, agentID)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, thread)
	}
}

// getThreadHandler gets a thread in the authenticated account.
// @Summary Get Thread
// @Description Retrieve one thread under the authenticated account.
// @Tags threads
// @Produce json
// @Param id path string true "Thread ID"
// @Success 200 {object} memory.Thread
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /threads/{id} [get]
func getThreadHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		threadID := strings.TrimSpace(r.PathValue("id"))
		if threadID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "thread id is required"})
			return
		}

		thread, err := agentSvc.GetThread(r.Context(), accountID, threadID)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, thread)
	}
}

// deleteThreadHandler deletes a thread from the authenticated account.
// @Summary Delete Thread
// @Description Delete one thread and all related thread-scoped records under the authenticated account.
// @Tags threads
// @Produce json
// @Param id path string true "Thread ID"
// @Success 200 {object} map[string]string "{"status":"deleted"}"
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /threads/{id} [delete]
func deleteThreadHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		threadID := strings.TrimSpace(r.PathValue("id"))
		if threadID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "thread id is required"})
			return
		}

		if err := agentSvc.DeleteThread(r.Context(), accountID, threadID); err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
