package whatsapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type SendMediaArgs struct {
	Recipient string `json:"recipient"`
	MediaURL  string `json:"mediaUrl"`
	MediaKind string `json:"mediaKind"` // image, audio, video, document, sticker
	Caption   string `json:"caption,omitempty"`
	FileName  string `json:"fileName,omitempty"`
	PTT       bool   `json:"ptt,omitempty"`
}

// SendText sends a plain text message to a WhatsApp recipient
func (m *WhatsAppManager) SendText(ctx context.Context, recipient, text string) (string, error) {
	if m.Client == nil || !m.Client.IsConnected() {
		return "", fmt.Errorf("whatsapp client is not connected")
	}

	jid, err := ResolveCanonicalJID(ctx, m.Client, recipient)
	if err != nil {
		return "", fmt.Errorf("failed to resolve JID: %w", err)
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	resp, err := m.Client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("failed to send text message: %w", err)
	}

	// Persist to database in background
	go func() {
		acctId, _ := m.DB.ResolveAccountId(context.Background())
		e164Phone := FormatE164(jid.User)
		contactId, _ := m.DB.FindOrCreateContact(context.Background(), acctId, e164Phone, e164Phone, "", jid.Server == types.GroupServer)
		if contactId != "" {
			convId, _ := m.DB.FindOrCreateConversation(context.Background(), acctId, contactId)
			if convId != "" {
				_ = m.DB.InsertMessage(context.Background(), convId, resp.ID, "agent", text, "", "text")
			}
		}
	}()

	return resp.ID, nil
}

// SendMedia sends image, audio, video or document media to a WhatsApp recipient
func (m *WhatsAppManager) SendMedia(ctx context.Context, args SendMediaArgs) (string, error) {
	if m.Client == nil || !m.Client.IsConnected() {
		return "", fmt.Errorf("whatsapp client is not connected")
	}

	jid, err := ResolveCanonicalJID(ctx, m.Client, args.Recipient)
	if err != nil {
		return "", fmt.Errorf("failed to resolve JID: %w", err)
	}

	// Download media bytes from provided URL
	req, err := http.NewRequestWithContext(ctx, "GET", args.MediaURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create media download request: %w", err)
	}

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download media from url: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media download failed with status %d", httpResp.StatusCode)
	}

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read media body: %w", err)
	}

	msg := &waE2E.Message{}
	kind := strings.ToLower(args.MediaKind)

	switch kind {
	case "image":
		uploaded, err := m.Client.Upload(ctx, data, whatsmeow.MediaImage)
		if err != nil {
			return "", fmt.Errorf("failed to upload image to whatsapp: %w", err)
		}
		msg.ImageMessage = &waE2E.ImageMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("image/jpeg"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		}
		if args.Caption != "" {
			msg.ImageMessage.Caption = proto.String(args.Caption)
		}

	case "audio":
		uploaded, err := m.Client.Upload(ctx, data, whatsmeow.MediaAudio)
		if err != nil {
			return "", fmt.Errorf("failed to upload audio to whatsapp: %w", err)
		}
		isPtt := args.PTT
		mimetype := "audio/ogg; codecs=opus"
		msg.AudioMessage = &waE2E.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String(mimetype),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			PTT:           proto.Bool(isPtt),
		}

	case "video":
		uploaded, err := m.Client.Upload(ctx, data, whatsmeow.MediaVideo)
		if err != nil {
			return "", fmt.Errorf("failed to upload video to whatsapp: %w", err)
		}
		msg.VideoMessage = &waE2E.VideoMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		}
		if args.Caption != "" {
			msg.VideoMessage.Caption = proto.String(args.Caption)
		}

	case "document":
		uploaded, err := m.Client.Upload(ctx, data, whatsmeow.MediaDocument)
		if err != nil {
			return "", fmt.Errorf("failed to upload document to whatsapp: %w", err)
		}
		fileName := args.FileName
		if fileName == "" {
			fileName = "document.pdf"
		}
		msg.DocumentMessage = &waE2E.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String(httpResp.Header.Get("Content-Type")),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			FileName:      proto.String(fileName),
		}
		if args.Caption != "" {
			msg.DocumentMessage.Caption = proto.String(args.Caption)
		}

	default:
		return "", fmt.Errorf("unsupported media kind: %s", args.MediaKind)
	}

	resp, err := m.Client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("failed to send media message: %w", err)
	}

	// Persist to database in background
	go func() {
		acctId, _ := m.DB.ResolveAccountId(context.Background())
		e164Phone := FormatE164(jid.User)
		contactId, _ := m.DB.FindOrCreateContact(context.Background(), acctId, e164Phone, e164Phone, "", jid.Server == types.GroupServer)
		if contactId != "" {
			convId, _ := m.DB.FindOrCreateConversation(context.Background(), acctId, contactId)
			if convId != "" {
				_ = m.DB.InsertMessage(context.Background(), convId, resp.ID, "agent", args.Caption, args.MediaURL, kind)
			}
		}
	}()

	return resp.ID, nil
}

// SendReaction sends an emoji reaction to a specific message
func (m *WhatsAppManager) SendReaction(ctx context.Context, recipient, targetMessageId, emoji string) error {
	if m.Client == nil || !m.Client.IsConnected() {
		return fmt.Errorf("whatsapp client is not connected")
	}

	jid, err := ResolveCanonicalJID(ctx, m.Client, recipient)
	if err != nil {
		return err
	}

	msg := m.Client.BuildReaction(jid, jid, targetMessageId, emoji)
	_, err = m.Client.SendMessage(ctx, jid, msg)
	return err
}

// SendPresence sends typing or paused indicator
func (m *WhatsAppManager) SendPresence(ctx context.Context, recipient, presence string) error {
	if m.Client == nil || !m.Client.IsConnected() {
		return fmt.Errorf("whatsapp client is not connected")
	}

	jid, err := ResolveCanonicalJID(ctx, m.Client, recipient)
	if err != nil {
		return err
	}

	chatPresence := types.ChatPresencePaused
	if presence == "composing" || presence == "typing" {
		chatPresence = types.ChatPresenceComposing
	}

	return m.Client.SendChatPresence(jid, chatPresence, types.ChatPresenceMediaText)
}
