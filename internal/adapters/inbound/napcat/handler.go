package napcat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type Processor func(context.Context, []byte) (any, error)

type Handler struct {
	accessToken string
	process     Processor
}

func NewHandler(accessToken string, process Processor) *Handler {
	return &Handler{
		accessToken: accessToken,
		process:     process,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.accessToken != "" && !authorized(r, h.accessToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	defer r.Body.Close()
	var payload json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.process(r.Context(), payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func authorized(r *http.Request, token string) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "Bearer "+token {
		return true
	}
	return strings.TrimSpace(r.Header.Get("X-Access-Token")) == token
}
