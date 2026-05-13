package httpresp

import (
	"encoding/json"
	"log"
	"net/http"
)

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpresp: encode failed: %v", err)
	}
}

func Err(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
