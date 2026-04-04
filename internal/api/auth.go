package api

import (
	"context"
	"net/http"
	"strings"

	"agentmem/internal/account"
	"agentmem/internal/agent"
)

type contextKey string

const accountIDContextKey contextKey = "account_id"

func requireAPIKey(accountSvc *account.Service, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimSpace(extractAPIKey(r))
		if apiKey == "" {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing api key"})
			return
		}
		key, err := accountSvc.GetAndValidateKey(r.Context(), apiKey)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "invalid api key"})
			return
		}
		ctx := context.WithValue(r.Context(), accountIDContextKey, key.AccountID)
		next(w, r.WithContext(ctx))
	}
}

func validateAgentOwnership(ctx context.Context, agentSvc *agent.Service, accountID, agentID string) error {
	_, err := agentSvc.GetAgent(ctx, accountID, agentID)
	return err
}

func accountIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(accountIDContextKey).(string)
	return strings.TrimSpace(value)
}

func extractAPIKey(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader != "" {
		const bearerPrefix = "Bearer "
		if strings.HasPrefix(strings.ToLower(authHeader), strings.ToLower(bearerPrefix)) {
			return strings.TrimSpace(authHeader[len(bearerPrefix):])
		}
		return authHeader
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}
