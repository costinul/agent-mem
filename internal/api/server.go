package api

import (
	"agentmem/internal/engine"
	"context"
	"net/http"

	_ "agentmem/docs"

	httpSwagger "github.com/swaggo/http-swagger"
)

type Server struct {
	httpServer *http.Server
	engine     *engine.MemoryEngine
}

func NewServer(engine *engine.MemoryEngine) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("POST /memory/contextual", contextualHandler(engine))
	mux.HandleFunc("POST /memory/factual", factualHandler(engine))
	mux.HandleFunc("GET /facts/{id}", getFactHandler(engine))
	mux.HandleFunc("PUT /facts/{id}", updateFactHandler(engine))
	mux.HandleFunc("DELETE /facts/{id}", deleteFactHandler(engine))
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	return &Server{
		httpServer: &http.Server{
			Handler: mux,
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
