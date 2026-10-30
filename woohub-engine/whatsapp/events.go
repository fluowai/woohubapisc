package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/events"
	"go.mau.fi/whatsmeow/types"
)

type WebhookPayload struct {
	Event     string                 `json:"event"`
	SessionId string                 `json:"sessionId"`
	Message   map[string]interface{} `json:"message"`
}

// EventHandler handles all incoming events from Whatsmeow
func (m *WhatsAppManager) EventHandler(rawEvt interface{}) {
	switch evt := rawEvt.(type) {
	case *events.Connected:
		log.Println("[Whatsmeow] Connected to WhatsApp network")
		m.mu.Lock()
		m.status = "connected"
		if m.Client != nil && m.Client.Store.ID != nil {
			m.activePhone = FormatE164(m.Client.Store.ID.User)
		}
		m.mu.Unlock()

		go func() {
			acctId, _ := m.DB.ResolveAccountId(context.Background())
			_ = m.DB.UpsertConnectedChannel(context.Background(), acctId, m.activePhone, "")
		}()

	case *events.LoggedOut:
		log.Printf("[Whatsmeow] Session logged out: %v", evt.Reason)
		m.mu.Lock()
		m.status = "disconnected"
		m.activePhone = ""
		m.mu.Unlock()

	case *events.Message:
		m.handleIncomingMessage(evt)
	}
}

func (m *WhatsAppManager) handleIncomingMessage(evt *events.Message) {
	// Skip status broadcasts and newsletters
	if evt.Info.Chat.Server == types.BroadcastServer || evt.Info.Chat.Server == types.NewsletterServer {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	msg := evt.Message
	if msg == nil {
		return
	}

	// Extract text content
	contentText := ""
	if conv := msg.GetConversation(); conv != "" {
		contentText = conv
	} else if ext := msg.GetExtendedTextMessage(); ext != nil {
		contentText = ext.GetText()
	} else if img := msg.GetImageMessage(); img != nil {
		contentText = img.GetCaption()
	} else if vid := msg.GetVideoMessage(); vid != nil {
		contentText = vid.GetCaption()
	} else if doc := msg.GetDocumentMessage(); doc != nil {
		contentText = doc.GetCaption()
	}

	// Process media (download decrypted bytes and upload to MinIO)
	mediaUrl, mediaType, err := m.ProcessIncomingMedia(ctx, msg, evt.Info.ID)
	if err != nil {
		log.Printf("[Whatsmeow] Media process notice for %s: %v", evt.Info.ID, err)
	}

	isGroup := evt.Info.Chat.Server == types.GroupServer
	remoteJid := evt.Info.Chat.String()
	senderPhone := FormatE164(evt.Info.Sender.User)
	fromMe := evt.Info.IsFromMe

	contactPhone := senderPhone
	if isGroup {
		contactPhone = remoteJid
	}

	contactName := evt.Info.PushName
	if contactName == "" || fromMe {
		contactName = contactPhone
	}

	// Fetch & store lead profile picture in MinIO if inbound
	avatarUrl := ""
	if !fromMe && !isGroup {
		avatar, err := m.FetchAndStoreLeadAvatar(ctx, evt.Info.Sender, senderPhone)
		if err == nil && avatar != "" {
			avatarUrl = avatar
		}
	}

	// Prefix group author name if needed
	if isGroup && !fromMe && evt.Info.PushName != "" && contentText != "" {
		if !strings.HasPrefix(contentText, "["+evt.Info.PushName+"]") {
			contentText = "[" + evt.Info.PushName + "]: " + contentText
		}
	}

	senderType := "customer"
	if fromMe {
		senderType = "agent"
	}

	// 1. Direct PostgreSQL Persistence
	go func() {
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dbCancel()

		acctId, _ := m.DB.ResolveAccountId(dbCtx)
		contactId, err := m.DB.FindOrCreateContact(dbCtx, acctId, contactPhone, contactName, avatarUrl, isGroup)
		if err != nil {
			log.Printf("[DB] FindOrCreateContact error: %v", err)
			return
		}

		convId, err := m.DB.FindOrCreateConversation(dbCtx, acctId, contactId)
		if err != nil {
			log.Printf("[DB] FindOrCreateConversation error: %v", err)
			return
		}

		err = m.DB.InsertMessage(dbCtx, convId, evt.Info.ID, senderType, contentText, mediaUrl, mediaType)
		if err != nil {
			log.Printf("[DB] InsertMessage error: %v", err)
		}
	}()

	// 2. Dispatch Webhook to Next.js CRM
	go m.dispatchWebhook(evt, contentText, mediaUrl, mediaType, avatarUrl, fromMe)
}

func (m *WhatsAppManager) dispatchWebhook(evt *events.Message, text, mediaUrl, mediaType, avatarUrl string, fromMe bool) {
	if m.Cfg.WebhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Event:     "messages.upsert",
		SessionId: "default",
		Message: map[string]interface{}{
			"id":          evt.Info.ID,
			"from":        evt.Info.Chat.String(),
			"fromMe":      fromMe,
			"pushName":    evt.Info.PushName,
			"timestamp":   evt.Info.Timestamp.Unix(),
			"content":     text,
			"text":        text,
			"type":        strings.ToUpper(mediaType),
			"mediaType":   mediaType,
			"mediaUrl":    mediaUrl,
			"fileUrl":     mediaUrl,
			"avatarUrl":   avatarUrl,
			"participant": evt.Info.Sender.String(),
			"key": map[string]interface{}{
				"remoteJid": evt.Info.Chat.String(),
				"fromMe":    fromMe,
				"id":        evt.Info.ID,
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", m.Cfg.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", m.Cfg.APIKey)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Also fallback to localhost:3000 if woocrm-app not reachable directly
		if strings.Contains(m.Cfg.WebhookURL, "woocrm-app") {
			fallbackURL := "http://localhost:3000/api/whatsapp/webhooks/whatsmeow"
			reqFallback, _ := http.NewRequest("POST", fallbackURL, bytes.NewBuffer(jsonData))
			if reqFallback != nil {
				reqFallback.Header.Set("Content-Type", "application/json")
				reqFallback.Header.Set("X-API-Key", m.Cfg.APIKey)
				_, _ = client.Do(reqFallback)
			}
		}
		return
	}
	defer resp.Body.Close()
}
