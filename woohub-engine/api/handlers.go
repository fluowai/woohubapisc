package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"whatsmeow/whatsapp"
)

type APIHandler struct {
	Manager *whatsapp.WhatsAppManager
}

func NewAPIHandler(mgr *whatsapp.WhatsAppManager) *APIHandler {
	return &APIHandler{Manager: mgr}
}

func (h *APIHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"engine":  "whatsmeow",
		"version": "1.0.0",
	})
}

// GetSessionStatus returns connection state, phone and QR
func (h *APIHandler) GetSessionStatus(w http.ResponseWriter, r *http.Request) {
	status, phone, qrDataURL, rawQR := h.Manager.GetStatus()

	resp := map[string]interface{}{
		"status":       status,
		"engineOnline": true,
		"phone":        phone,
		"qr":           rawQR,
		"qrCode":       qrDataURL,
		"base64":       qrDataURL,
	}

	if phone != "" {
		digits := strings.TrimPrefix(phone, "+")
		resp["me"] = map[string]interface{}{
			"id":   fmt.Sprintf("%s@s.whatsapp.net", digits),
			"name": phone,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetSessionQR returns the active QR code
func (h *APIHandler) GetSessionQR(w http.ResponseWriter, r *http.Request) {
	status, phone, qrDataURL, rawQR := h.Manager.GetStatus()

	if status == "connected" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "connected",
			"phone":  phone,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "scan_qr",
		"qr":     rawQR,
		"qrCode": qrDataURL,
		"base64": qrDataURL,
		"data":   qrDataURL,
	})
}

// StartSession triggers session start and QR code generation
func (h *APIHandler) StartSession(w http.ResponseWriter, r *http.Request) {
	err := h.Manager.StartSession(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	status, phone, qrDataURL, rawQR := h.Manager.GetStatus()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"status":  status,
		"phone":   phone,
		"qr":      rawQR,
		"qrCode":  qrDataURL,
	})
}

// StopSession logs out and stops session
func (h *APIHandler) StopSession(w http.ResponseWriter, r *http.Request) {
	err := h.Manager.Logout(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"status":  "disconnected",
	})
}

// SendMessage handles text and media sending
func (h *APIHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	recipient := vars["recipient"]

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if recipient == "" {
		if rec, ok := body["recipient"].(string); ok {
			recipient = rec
		} else if to, ok := body["to"].(string); ok {
			recipient = to
		}
	}

	if recipient == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipient required"})
		return
	}

	// 1. Check for text message
	msgObj, hasMsgObj := body["message"].(map[string]interface{})
	var textContent string
	if hasMsgObj {
		if t, ok := msgObj["text"].(string); ok {
			textContent = t
		}
	} else if t, ok := body["text"].(string); ok {
		textContent = t
	}

	if textContent != "" {
		msgId, err := h.Manager.SendText(r.Context(), recipient, textContent)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":        msgId,
			"messageId": msgId,
			"status":    "sent",
		})
		return
	}

	// 2. Check for media message
	var mediaUrl, mediaKind, caption, fileName string
	var ptt bool

	if hasMsgObj {
		if img, ok := msgObj["image"].(map[string]interface{}); ok {
			mediaKind = "image"
			mediaUrl, _ = img["url"].(string)
		} else if aud, ok := msgObj["audio"].(map[string]interface{}); ok {
			mediaKind = "audio"
			mediaUrl, _ = aud["url"].(string)
			ptt, _ = aud["ptt"].(bool)
		} else if vid, ok := msgObj["video"].(map[string]interface{}); ok {
			mediaKind = "video"
			mediaUrl, _ = vid["url"].(string)
		} else if doc, ok := msgObj["document"].(map[string]interface{}); ok {
			mediaKind = "document"
			mediaUrl, _ = doc["url"].(string)
			fileName, _ = msgObj["fileName"].(string)
		}
		caption, _ = msgObj["caption"].(string)
	} else {
		mediaUrl, _ = body["mediaUrl"].(string)
		mediaKind, _ = body["mediaKind"].(string)
		caption, _ = body["caption"].(string)
		fileName, _ = body["fileName"].(string)
		ptt, _ = body["ptt"].(bool)
	}

	if mediaUrl != "" && mediaKind != "" {
		msgId, err := h.Manager.SendMedia(r.Context(), whatsapp.SendMediaArgs{
			Recipient: recipient,
			MediaURL:  mediaUrl,
			MediaKind: mediaKind,
			Caption:   caption,
			FileName:  fileName,
			PTT:       ptt,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":        msgId,
			"messageId": msgId,
			"status":    "sent",
		})
		return
	}

	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no supported message payload found"})
}

// ReactMessage handles emoji reactions
func (h *APIHandler) ReactMessage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	recipient := vars["recipient"]
	messageId := vars["messageId"]

	var body struct {
		Emoji string `json:"emoji"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if recipient == "" || messageId == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing recipient or messageId"})
		return
	}

	err := h.Manager.SendReaction(r.Context(), recipient, messageId, body.Emoji)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ChatPresence handles typing indicator
func (h *APIHandler) ChatPresence(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	recipient := vars["recipient"]

	var body struct {
		Presence string `json:"presence"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	err := h.Manager.SendPresence(r.Context(), recipient, body.Presence)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// GetProfilePicture fetches lead profile picture, uploads to MinIO, and returns the URL
func (h *APIHandler) GetProfilePicture(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	recipient := vars["recipient"]
	if recipient == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing recipient"})
		return
	}

	jid, err := whatsapp.ResolveCanonicalJID(r.Context(), h.Manager.Client, recipient)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	phone := whatsapp.FormatE164(jid.User)
	url, err := h.Manager.FetchAndStoreLeadAvatar(r.Context(), jid, phone)
	if err != nil || url == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"profilePicUrl": nil})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"profilePicUrl": url,
	})
}

// ResolveJID tests and returns canonical WhatsApp JID
func (h *APIHandler) ResolveJID(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	phone := vars["phone"]
	if phone == "" {
		var body struct {
			Phone string `json:"phone"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		phone = body.Phone
	}

	if phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone required"})
		return
	}

	jid, err := whatsapp.ResolveCanonicalJID(r.Context(), h.Manager.Client, phone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"phone":        phone,
		"jid":          jid.String(),
		"user":         jid.User,
		"server":       jid.Server,
		"formatted":    whatsapp.FormatE164(jid.User),
		"isOnWhatsApp": true,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
