package api

import (
	"agentmem/internal/account"
	"agentmem/internal/agent"
	"agentmem/internal/engine"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	_ "agentmem/docs"

	httpSwagger "github.com/swaggo/http-swagger"
)

type Server struct {
	httpServer *http.Server
	engine     *engine.MemoryEngine
}

func NewServer(engine *engine.MemoryEngine, accountSvc *account.Service, agentSvc *agent.Service) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("POST /memory/contextual", requireAPIKey(accountSvc, contextualHandler(engine, agentSvc)))
	mux.HandleFunc("POST /memory/factual", requireAPIKey(accountSvc, factualHandler(engine, agentSvc)))
	mux.HandleFunc("POST /memory/recall", requireAPIKey(accountSvc, recallHandler(engine, agentSvc)))
	mux.HandleFunc("GET /facts", requireAPIKey(accountSvc, listFactsHandler(engine)))
	mux.HandleFunc("GET /facts/{id}", requireAPIKey(accountSvc, getFactHandler(engine)))
	mux.HandleFunc("PUT /facts/{id}", requireAPIKey(accountSvc, updateFactHandler(engine)))
	mux.HandleFunc("DELETE /facts/{id}", requireAPIKey(accountSvc, deleteFactHandler(engine)))
	mux.HandleFunc("POST /agents", requireAPIKey(accountSvc, createAgentHandler(agentSvc)))
	mux.HandleFunc("GET /agents/{id}", requireAPIKey(accountSvc, getAgentHandler(agentSvc)))
	mux.HandleFunc("DELETE /agents/{id}", requireAPIKey(accountSvc, deleteAgentHandler(agentSvc)))
	mux.HandleFunc("POST /threads", requireAPIKey(accountSvc, createThreadHandler(agentSvc)))
	mux.HandleFunc("GET /threads/{id}", requireAPIKey(accountSvc, getThreadHandler(agentSvc)))
	mux.HandleFunc("DELETE /threads/{id}", requireAPIKey(accountSvc, deleteThreadHandler(agentSvc)))
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.PersistAuthorization(true)))

	return &Server{
		httpServer: &http.Server{
			Handler: withRecovery(mux),
		},
		engine: engine,
	}
}

func (s *Server) Start(port string) error {
	s.httpServer.Addr = ":" + port
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error(
					"api panic recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"account", accountIDFromContext(r.Context()),
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
				)
				writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
