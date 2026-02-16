package setup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	Port string
}

func NewServer(port string) *Server {
	if port == "" {
		port = "8080"
	}
	return &Server{Port: port}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	wizard := NewWizard()

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := map[string]string{
			"status": "setup_required",
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	r.Get("/setup/welcome", wizard.HandleWelcome)
	r.Get("/setup/database", wizard.HandleDatabase)
	r.Post("/setup/database", wizard.HandleDatabase)
	r.Get("/setup/services", wizard.HandleServices)
	r.Post("/setup/services", wizard.HandleServices)
	r.Get("/setup/storage", wizard.HandleStorage)
	r.Post("/setup/storage", wizard.HandleStorage)
	r.Get("/setup/security", wizard.HandleSecurity)
	r.Post("/setup/security", wizard.HandleSecurity)
	r.Get("/setup/review", wizard.HandleReview)
	r.Post("/setup/review", wizard.HandleReview)
	r.Get("/setup/complete", wizard.HandleComplete)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/setup/welcome", http.StatusSeeOther)
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		response := map[string]string{
			"error":   "setup_required",
			"message": "Application setup is not complete. Please complete the initial setup.",
		}
		_ = json.NewEncoder(w).Encode(response)
	})

	return r
}

func (s *Server) Start() error {
	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", s.Port),
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Setup mode: Server starting on port %s", s.Port)
	log.Printf("Setup mode: Complete initial setup to start the application")

	return server.ListenAndServe()
}
