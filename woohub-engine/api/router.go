package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"whatsmeow/config"
	"whatsmeow/whatsapp"
)

func SetupRouter(cfg *config.Config, mgr *whatsapp.WhatsAppManager) *mux.Router {
	r := mux.NewRouter()
	handler := NewAPIHandler(mgr)

	// Auth Middleware
	authMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Allow health and QR check without auth if needed, or check API key
			if req.URL.Path == "/health" {
				next.ServeHTTP(w, req)
				return
			}

			key := req.Header.Get("X-API-Key")
			if key == "" {
				authHeader := req.Header.Get("Authorization")
				if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
					key = authHeader[7:]
				}
			}

			// If key provided or configured, check match
			if cfg.APIKey != "" && key != "" && key != cfg.APIKey {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
				return
			}

			next.ServeHTTP(w, req)
		})
	}

	r.Use(authMiddleware)

	// Health
	r.HandleFunc("/health", handler.HealthCheck).Methods("GET")

	// Session inspection & QR
	r.HandleFunc("/api/session/status", handler.GetSessionStatus).Methods("GET")
	r.HandleFunc("/api/sessions/{sessionId}", handler.GetSessionStatus).Methods("GET")
	r.HandleFunc("/api/session/qr", handler.GetSessionQR).Methods("GET")
	r.HandleFunc("/api/sessions/{sessionId}/qr", handler.GetSessionQR).Methods("GET")

	// Session management
	r.HandleFunc("/api/sessions", handler.StartSession).Methods("POST")
	r.HandleFunc("/api/sessions/{sessionId}", handler.StartSession).Methods("POST")
	r.HandleFunc("/api/sessions/{sessionId}/start", handler.StartSession).Methods("POST")
	r.HandleFunc("/api/session/start", handler.StartSession).Methods("POST")
	r.HandleFunc("/api/sessions/{sessionId}/stop", handler.StopSession).Methods("POST")
	r.HandleFunc("/api/session/logout", handler.StopSession).Methods("POST")

	// Messaging
	r.HandleFunc("/api/messages/{sessionId}/{recipient}/send", handler.SendMessage).Methods("POST")
	r.HandleFunc("/api/send/text", handler.SendMessage).Methods("POST")
	r.HandleFunc("/api/send/media", handler.SendMessage).Methods("POST")

	// Reactions
	r.HandleFunc("/api/messages/{sessionId}/{recipient}/{messageId}/react", handler.ReactMessage).Methods("POST")

	// Chat presence (typing indicator)
	r.HandleFunc("/api/chat/{sessionId}/{recipient}/presence", handler.ChatPresence).Methods("POST")

	// Lead avatar
	r.HandleFunc("/api/chat/{sessionId}/{recipient}/profile-picture", handler.GetProfilePicture).Methods("GET", "POST")

	// Canonical WhatsApp JID resolver
	r.HandleFunc("/api/contacts/resolve-jid", handler.ResolveJID).Methods("POST")
	r.HandleFunc("/api/contacts/{phone}/resolve", handler.ResolveJID).Methods("GET")

	return r
}
