package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"agentmem/internal/errs"

	"agentmem/internal/agent"
	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

type apiError struct {
	Error string `json:"error"`
}

// contextualHandler handles the contextual smart pipeline.
// @Summary Process Contextual Memory
// @Description Process input through the contextual smart pipeline to store, update, and evolve facts.
// @Tags memory
// @Accept json
// @Produce json
// @Param input body memory.MemoryInput true "Memory Input"
// @Success 200 {object} memory.WriteOutput
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /memory/contextual [post]
func contextualHandler(memEngine *engine.MemoryEngine, agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		var input models.MemoryInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}
		input.AccountID = accountID
		if err := populateAgentFromThread(r, agentSvc, &input.AgentID, input.ThreadID); err != nil {
			writeEngineError(w, r, err)
			return
		}
		output, err := memEngine.ProcessContextual(r.Context(), input)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

// factualHandler handles the factual interface.
// @Summary Add Factual Memory
// @Description Process input through the factual pipeline to store, update, and evolve facts (no conversation context).
// @Tags memory
// @Accept json
// @Produce json
// @Param input body memory.FactualInput true "Factual Input"
// @Success 200 {object} memory.WriteOutput
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /memory/factual [post]
func factualHandler(memEngine *engine.MemoryEngine, agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		var input models.FactualInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}
		input.AccountID = accountID
		if err := populateAgentFromThread(r, agentSvc, &input.AgentID, input.ThreadID); err != nil {
			writeEngineError(w, r, err)
			return
		}
		output, err := memEngine.AddFactual(r.Context(), input)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

// recallHandler handles read-only memory retrieval by semantic search.
// @Summary Recall Memory
// @Description Retrieve relevant facts by semantic similarity to a query. No storage or mutation.
// @Tags memory
// @Accept json
// @Produce json
// @Param input body memory.RecallInput true "Recall Input"
// @Success 200 {object} memory.RecallOutput
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /memory/recall [post]
func recallHandler(memEngine *engine.MemoryEngine, agentSvc *agent.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		var input models.RecallInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}
		input.AccountID = accountID
		input.Debug = debugFromContext(r.Context())
		if input.AgentID != "" {
			if err := validateAgentOwnership(r.Context(), agentSvc, accountID, input.AgentID); err != nil {
				writeEngineError(w, r, err)
				return
			}
		}
		if input.ThreadID != "" {
			if _, err := agentSvc.GetThread(r.Context(), accountID, input.ThreadID); err != nil {
				writeEngineError(w, r, err)
				return
			}
		}
		output, err := memEngine.Recall(r.Context(), input)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

// getFactHandler retrieves a specific fact by ID.
// @Summary Get Fact
// @Description Retrieve a specific fact by its ID, optionally including original sources.
// @Tags facts
// @Produce json
// @Param id path string true "Fact ID"
// @Param include_sources query bool false "Include original sources"
// @Success 200 {object} memory.Fact
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /facts/{id} [get]
func getFactHandler(memEngine *engine.MemoryEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		factID := strings.TrimSpace(r.PathValue("id"))
		if factID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "fact id is required"})
			return
		}

		includeSources := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_sources")), "true")
		fact, err := memEngine.GetFactForAccount(r.Context(), accountID, factID, includeSources)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, fact)
	}
}

// updateFactHandler updates an existing fact.
// @Summary Update Fact
// @Description Update the text and source of an existing fact.
// @Tags facts
// @Accept json
// @Produce json
// @Param id path string true "Fact ID"
// @Param body body memory.FactUpdateBody true "Update Body"
// @Success 200 {object} memory.Fact
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /facts/{id} [put]
func updateFactHandler(memEngine *engine.MemoryEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		factID := strings.TrimSpace(r.PathValue("id"))
		if factID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "fact id is required"})
			return
		}

		var body models.FactUpdateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON payload"})
			return
		}
		if strings.TrimSpace(body.Text) == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "text is required"})
			return
		}
		if _, ok := models.SourceTrustHierarchy[body.Source]; !ok {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "source is invalid"})
			return
		}

		fact, err := memEngine.UpdateFactForAccount(r.Context(), accountID, factID, body.Text, body.Source)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, fact)
	}
}

// listFactsHandler lists facts with optional filters.
// @Summary List Facts
// @Description List facts scoped to the account, with optional agent, thread, and kind filters.
// @Tags facts
// @Produce json
// @Param agent_id query string false "Agent ID"
// @Param thread_id query string false "Thread ID"
// @Param kind query string false "Fact kind (KNOWLEDGE, RULE, PREFERENCE)"
// @Param limit query int false "Max results (default 50)"
// @Param offset query int false "Offset for pagination"
// @Success 200 {object} memory.FactListOutput
// @Failure 401 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /facts [get]
func listFactsHandler(memEngine *engine.MemoryEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		q := r.URL.Query()
		params := memoryrepo.ListFactsParams{}
		if v := strings.TrimSpace(q.Get("agent_id")); v != "" {
			params.AgentID = &v
		}
		if v := strings.TrimSpace(q.Get("thread_id")); v != "" {
			params.ThreadID = &v
		}
		if v := strings.TrimSpace(q.Get("kind")); v != "" {
			kind := models.FactKind(v)
			params.Kind = &kind
		}
		if v := q.Get("limit"); v != "" {
			fmt.Sscanf(v, "%d", &params.Limit)
		}
		if v := q.Get("offset"); v != "" {
			fmt.Sscanf(v, "%d", &params.Offset)
		}

		output, err := memEngine.ListFactsForAccount(r.Context(), accountID, params)
		if err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

// deleteFactHandler deletes a fact by ID.
// @Summary Delete Fact
// @Description Delete a specific fact by its ID.
// @Tags facts
// @Produce json
// @Param id path string true "Fact ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} apiError
// @Failure 401 {object} apiError
// @Failure 404 {object} apiError
// @Failure 500 {object} apiError
// @Security ApiKeyAuth
// @Router /facts/{id} [delete]
func deleteFactHandler(memEngine *engine.MemoryEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := accountIDFromContext(r.Context())
		if accountID == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing account context"})
			return
		}
		factID := strings.TrimSpace(r.PathValue("id"))
		if factID == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "fact id is required"})
			return
		}
		if err := memEngine.DeleteFactForAccount(r.Context(), accountID, factID); err != nil {
			writeEngineError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeEngineError(w http.ResponseWriter, r *http.Request, err error) {
	method := ""
	path := ""
	accountID := ""
	if r != nil {
		method = r.Method
		path = r.URL.Path
		accountID = accountIDFromContext(r.Context())
	}

	var valErr *errs.ValidationError
	var notFoundErr *errs.NotFoundError
	switch {
	case errors.As(err, &valErr):
		slog.Warn("api validation error",
			"method", method,
			"path", path,
			"account", accountID,
			"error", err.Error(),
		)
		writeJSON(w, http.StatusBadRequest, apiError{Error: valErr.Error()})
	case errors.As(err, &notFoundErr):
		slog.Warn("api not found",
			"method", method,
			"path", path,
			"account", accountID,
			"error", err.Error(),
		)
		writeJSON(w, http.StatusNotFound, apiError{Error: notFoundErr.Error()})
	default:
		slog.Error("api internal error",
			"method", method,
			"path", path,
			"account", accountID,
			"error", err.Error(),
			"stack", string(debug.Stack()),
		)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal server error"})
	}
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func populateAgentFromThread(r *http.Request, agentSvc *agent.Service, agentID *string, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	thread, err := agentSvc.GetThread(r.Context(), accountIDFromContext(r.Context()), threadID)
	if err != nil {
		return err
	}
	*agentID = thread.AgentID
	return nil
}
