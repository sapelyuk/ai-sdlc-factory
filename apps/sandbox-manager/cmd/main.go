// Command sandboxmanager is a build stub for the ai-sdlc-factory sandbox-manager service.
//
// It serves only GET /healthz so that the Docker Compose stack can build and
// start. The real implementation lands in M1 (see issue #96).
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":" + getenv("PORT", "8000")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("ai-sdlc-factory-sandbox-manager stub listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// getenv returns the value of key, or fallback when it is empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
