package handler

import (
	"encoding/json"
	"net/http"
)

// HealthHandler GET /health — 存活探针，供 K8s liveness probe 使用。
func HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
