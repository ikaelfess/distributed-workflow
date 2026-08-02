package httpapi

import (
	"net/http"
	"strings"
)

type OriginPolicy struct {
	allowed map[string]struct{}
}

func NewOriginPolicy(origins []string) OriginPolicy {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		allowed[origin] = struct{}{}
	}
	return OriginPolicy{allowed: allowed}
}

func (p OriginPolicy) Allows(origin string) bool {
	if origin == "" {
		return false
	}
	_, ok := p.allowed[origin]
	return ok
}

func withCORS(next http.Handler, policy OriginPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if policy.Allows(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, "+CSRFTokenHeaderName,
			)
			w.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, DELETE, OPTIONS",
			)
		}

		if r.Method == http.MethodOptions {
			if !policy.Allows(origin) {
				writeProblem(
					w,
					http.StatusForbidden,
					"origin-forbidden",
					"origin forbidden",
					"the request origin is not allowed",
				)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func requireAllowedOrigin(w http.ResponseWriter, r *http.Request, policy OriginPolicy) bool {
	origin := r.Header.Get("Origin")
	if !policy.Allows(origin) {
		writeProblem(
			w,
			http.StatusForbidden,
			"origin-forbidden",
			"origin forbidden",
			"the request origin is not allowed",
		)
		return false
	}
	return true
}

func requireCSRFHeader(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := strings.TrimSpace(r.Header.Get(CSRFTokenHeaderName))
	if token == "" {
		writeProblem(
			w,
			http.StatusForbidden,
			"csrf-failed",
			"csrf failed",
			"the csrf token is missing or invalid",
		)
		return "", false
	}
	return token, true
}
