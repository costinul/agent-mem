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

// createSessionHandler creates a session for an agent in the authenticated account.
// @Summary Create Session
// @Description Create a new session for an agent under the authenticated account.
// @Tags sessions
// @Produce json
// @Param agentId path string true "Agent ID"
// @Success 201 {object} memory.Session
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents/{agentId}/sessions [post]
func createSessionHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		agentID := strings.TrimSpace(r.PathValue("agentId"))
		if agentID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id is required"})
			return
		}

		session, err := agentSvc.CreateSession(r.Context(), accountID, agentID)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, session)
	}
}

// getSessionHandler gets a session for an agent in the authenticated account.
// @Summary Get Session
// @Description Retrieve one session for an agent under the authenticated account.
// @Tags sessions
// @Produce json
// @Param agentId path string true "Agent ID"
// @Param id path string true "Session ID"
// @Success 200 {object} memory.Session
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents/{agentId}/sessions/{id} [get]
func getSessionHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		agentID := strings.TrimSpace(r.PathValue("agentId"))
		sessionID := strings.TrimSpace(r.PathValue("id"))
		if agentID == "" || sessionID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id and session id are required"})
			return
		}

		session, err := agentSvc.GetSession(r.Context(), accountID, agentID, sessionID)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
	}
}

// deleteSessionHandler closes a session for an agent in the authenticated account.
// @Summary Delete Session
// @Description Close one session for an agent under the authenticated account.
// @Tags sessions
// @Produce json
// @Param agentId path string true "Agent ID"
// @Param id path string true "Session ID"
// @Success 200 {object} map[string]string "{"status":"deleted"}"
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /agents/{agentId}/sessions/{id} [delete]
func deleteSessionHandler(agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		agentID := strings.TrimSpace(r.PathValue("agentId"))
		sessionID := strings.TrimSpace(r.PathValue("id"))
		if agentID == "" || sessionID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "agent id and session id are required"})
			return
		}

		if err := agentSvc.CloseSession(r.Context(), accountID, agentID, sessionID); err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
