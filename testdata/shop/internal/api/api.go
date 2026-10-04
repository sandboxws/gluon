// Package api is the shop's HTTP API.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"example.com/shop/internal/store"
)

// Routes is the API. The handlers read s only when a request arrives, so the
// routes can be listed with no database behind them.
func Routes(s *store.Store) chi.Router {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Route("/users", func(r chi.Router) {
		r.Get("/", listUsers(s))
		r.Get("/{id}", getUser(s))
		r.Get("/{id}/orders", userOrders(s))
	})
	r.Route("/orders", func(r chi.Router) {
		r.Post("/", notYet)
		r.Post("/{id}/refund", notYet)
	})
	r.Mount("/admin", admin())
	return r
}

func admin() chi.Router {
	r := chi.NewRouter()
	r.Get("/stats", notYet)
	r.Delete("/cache", notYet)
	return r
}

func listUsers(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		us, err := s.Users(r.Context())
		if err != nil {
			reply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		reply(w, http.StatusOK, us)
	}
}

func getUser(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": "id is a number"})
			return
		}
		u, err := s.User(r.Context(), id)
		if err != nil {
			reply(w, http.StatusNotFound, map[string]string{"error": "no such user"})
			return
		}
		reply(w, http.StatusOK, u)
	}
}

func userOrders(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		os, err := s.Orders(r.Context(), id)
		if err != nil {
			reply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		reply(w, http.StatusOK, os)
	}
}

func notYet(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusNotImplemented, map[string]string{"error": "not implemented"})
}

// reply writes v as JSON. The Date header is left off, so a response reads
// the same every time it is fetched.
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header()["Date"] = nil
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
