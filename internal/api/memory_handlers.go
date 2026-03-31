package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentmem/internal/agent"
	"agentmem/internal/engine"
	models "agentmem/internal/models"
)

type apiError struct {
	Error string `json:"error"`
}

// contextualHandler handles the contextual smart pipeline.
// @Summary Process Contextual Memory
// @Description Process input through the contextual smart pipeline to retrieve relevant facts and messages.
// @Tags memory
// @Accept json
// @Produce json
// @Param input body memory.MemoryInput true "Memory Input"
// @Success 200 {object} memory.MemoryOutput
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
			writeEngineError(w, err)
			return
		}
		output, err := memEngine.ProcessContextual(r.Context(), input)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, output)
	}
}

// factualHandler handles the factual interface.
// @Summary Add Factual Memory
// @Description Add new facts to the memory engine.
// @Tags memory
// @Accept json
// @Produce json
// @Param input body memory.FactualInput true "Factual Input"
// @Success 200 {object} memory.EvaluateResult
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
			writeEngineError(w, err)
			return
		}
		output, err := memEngine.AddFactual(r.Context(), input)
		if err != nil {
			writeEngineError(w, err)
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
			writeEngineError(w, err)
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
			writeEngineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, fact)
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeEngineError(w http.ResponseWriter, err error) {
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(strings.ToLower(message), "not found"):
		writeJSON(w, http.StatusNotFound, apiError{Error: message})
	case strings.Contains(strings.ToLower(message), "required"),
		strings.Contains(strings.ToLower(message), "invalid"),
		strings.Contains(strings.ToLower(message), "immutable"),
		strings.Contains(strings.ToLower(message), "not allowed"):
		writeJSON(w, http.StatusBadRequest, apiError{Error: message})
	default:
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
