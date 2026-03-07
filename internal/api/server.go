package api

import (
	"context"
	"net/http"

	"agentmem/internal/database"
)

type Server struct {
	httpServer *http.Server
	db         *database.DB
}

func NewServer(db *database.DB) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)

	return &Server{
		httpServer: &http.Server{
			Handler: mux,
		},
		db:    db,
	}
}

func (s *Server) Start(port string) error {
	s.httpServer.Addr = ":" + port
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
