package api

import (
	"context"
	"net/http"

	"agentmem/internal/memengine"
)

type Server struct {
	httpServer *http.Server
	engine     *memengine.MemoryEngine
}

func NewServer(engine *memengine.MemoryEngine) *Server {
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
