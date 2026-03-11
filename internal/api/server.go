package api

import (
	"agentmem/internal/engine"
	"context"
	"net/http"
)

type Server struct {
	httpServer *http.Server
	engine     *engine.MemoryEngine
}

func NewServer(engine *engine.MemoryEngine) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)

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
