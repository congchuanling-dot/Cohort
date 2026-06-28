package main

import (
	"encoding/json"
	"log"
	"net/http"
)

// HealthResponse represents the health check JSON response.
type HealthResponse struct {
	Status string `json:"status"`
}

// healthHandler responds to GET /health with {"status":"ok"}.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests; reject others with 405 Method Not Allowed.
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := HealthResponse{Status: "ok"}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to encode health response: %v", err)
	}
}

func main() {
	// Register the /health route.
	http.HandleFunc("/health", healthHandler)

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("server failed to start: %v", err)
	}
}
