package api

import (
	"log"
	"net/http"
	"time"
)

// withMiddleware applies logging and panic recovery.
func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				http.Error(w, `{"error":{"code":"internal","message":"internal error","request_id":""}}`,
					http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
