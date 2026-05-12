package api

import (
	"agentmem/internal/account"
	"agentmem/internal/agent"
	"agentmem/internal/auth"
	"agentmem/internal/engine"
	"agentmem/internal/ownerhub"
	"agentmem/internal/repository/userrepo"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	_ "agentmem/docs"

	httpSwagger "github.com/swaggo/http-swagger"
)

type Server struct {
	httpServer *http.Server
	engine     *engine.MemoryEngine
}

type OwnerHubDeps struct {
	GoogleAuth   *auth.GoogleAuth
	SessionStore auth.SessionStore
	UserRepo     userrepo.Repository
	Handler      *ownerhub.Handler
}

func NewServer(engine *engine.MemoryEngine, accountSvc *account.Service, agentSvc *agent.Service, ownerhubDeps *OwnerHubDeps) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("POST /memory", requireAPIKey(accountSvc, addHandler(engine, agentSvc)))
	mux.HandleFunc("POST /memory/recall", requireAPIKey(accountSvc, recallHandler(engine, agentSvc)))
	mux.HandleFunc("POST /memory/recall/light", requireAPIKey(accountSvc, recallLightHandler(engine, agentSvc)))
	mux.HandleFunc("POST /memory/recall/zero", requireAPIKey(accountSvc, recallZeroHandler(engine, agentSvc)))
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
	mux.HandleFunc("GET /threads/{id}/messages", requireAPIKey(accountSvc, listThreadMessagesHandler(agentSvc, engine)))
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.PersistAuthorization(true)))

	if ownerhubDeps != nil {
		mux.HandleFunc("GET /auth/google/login", ownerhubDeps.GoogleAuth.LoginHandler)
		mux.HandleFunc("GET /auth/google/callback", ownerhubDeps.GoogleAuth.CallbackHandler)
		mux.HandleFunc("GET /auth/logout", ownerhubDeps.GoogleAuth.LogoutHandler)

		ownerhubMw := func(next http.Handler) http.Handler {
			return auth.RequireOwnerHub(ownerhubDeps.SessionStore, ownerhubDeps.UserRepo, next)
		}
		ownerhubDeps.Handler.RegisterRoutes(mux, ownerhubMw)
	}

	return &Server{
		httpServer: &http.Server{
			Handler: withRecovery(withAccessLog(mux)),
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

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &statusResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(writer, r)

		slog.Debug(
			"http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", writer.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"account", accountIDFromContext(r.Context()),
		)
	})
}
