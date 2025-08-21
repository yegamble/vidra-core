// Package generated provides server interfaces and routing generated from OpenAPI specification.
// Code generated from OpenAPI spec. DO NOT EDIT.
package generated

import (
	"net/http"

	chi "github.com/go-chi/chi/v5"
)

// ServerInterface represents all server handlers.
type ServerInterface interface {
	// Register a new user
	// (POST /auth/register)
	Register(w http.ResponseWriter, r *http.Request)

	// Authenticate user
	// (POST /auth/login)
	Login(w http.ResponseWriter, r *http.Request)

	// Refresh access token
	// (POST /auth/refresh)
	RefreshToken(w http.ResponseWriter, r *http.Request)

	// Logout user
	// (POST /auth/logout)
	Logout(w http.ResponseWriter, r *http.Request)

	// Health check
	// (GET /health)
	HealthCheck(w http.ResponseWriter, r *http.Request)

	// Readiness check
	// (GET /ready)
	ReadinessCheck(w http.ResponseWriter, r *http.Request)
}

// ServerInterfaceWrapper converts contexts to parameters.
type ServerInterfaceWrapper struct {
	Handler ServerInterface
}

// Register wraps the Register handler
func (siw *ServerInterfaceWrapper) Register(w http.ResponseWriter, r *http.Request) {
	siw.Handler.Register(w, r)
}

// Login wraps the Login handler
func (siw *ServerInterfaceWrapper) Login(w http.ResponseWriter, r *http.Request) {
	siw.Handler.Login(w, r)
}

// RefreshToken wraps the RefreshToken handler
func (siw *ServerInterfaceWrapper) RefreshToken(w http.ResponseWriter, r *http.Request) {
	siw.Handler.RefreshToken(w, r)
}

// Logout wraps the Logout handler
func (siw *ServerInterfaceWrapper) Logout(w http.ResponseWriter, r *http.Request) {
	siw.Handler.Logout(w, r)
}

// HealthCheck wraps the HealthCheck handler
func (siw *ServerInterfaceWrapper) HealthCheck(w http.ResponseWriter, r *http.Request) {
	siw.Handler.HealthCheck(w, r)
}

// ReadinessCheck wraps the ReadinessCheck handler
func (siw *ServerInterfaceWrapper) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	siw.Handler.ReadinessCheck(w, r)
}

// Handler creates an http.Handler with routing matching OpenAPI spec.
func Handler(si ServerInterface) http.Handler {
	return HandlerFromMux(si, chi.NewRouter())
}

// HandlerFromMux creates an http.Handler with routing matching OpenAPI spec based on provided mux.
func HandlerFromMux(si ServerInterface, r chi.Router) http.Handler {
	wrapper := ServerInterfaceWrapper{
		Handler: si,
	}

	r.Post("/auth/register", wrapper.Register)
	r.Post("/auth/login", wrapper.Login)
	r.Post("/auth/refresh", wrapper.RefreshToken)
	r.Post("/auth/logout", wrapper.Logout)
	r.Get("/health", wrapper.HealthCheck)
	r.Get("/ready", wrapper.ReadinessCheck)

	return r
}

// HandlerWithOptions creates an http.Handler with additional options
func HandlerWithOptions(si ServerInterface, options ChiServerOptions) http.Handler {
	var r chi.Router = chi.NewRouter()

	if options.BaseRouter != nil {
		r = options.BaseRouter
	}

	return HandlerFromMux(si, r)
}

// ChiServerOptions provides options for the chi server
type ChiServerOptions struct {
	BaseRouter chi.Router
}
