package whatsapp

import (
	"context"
	"fmt"
	"log"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// ProcessIncomingMedia downloads decrypted media from WhatsApp and uploads it directly to MinIO
func (m *WhatsAppManager) ProcessIncomingMedia(ctx context.Context, msg *waE2E.Message, messageId string) (mediaUrl, mediaType string, err error) {
	if m.Client == nil {
		return "", "", fmt.Errorf("client not initialized")
	}

	if img := msg.GetImageMessage(); img != nil {
		mediaType = "image"
		data, err := m.Client.Download(img)
		if err != nil {
			return "", mediaType, fmt.Errorf("failed to download image: %w", err)
		}
		filename := fmt.Sprintf("%s.jpg", messageId)
		url, err := m.Storage.UploadMedia(ctx, data, filename, "image/jpeg")
		return url, mediaType, err
	}

	if aud := msg.GetAudioMessage(); aud != nil {
		mediaType = "audio"
		data, err := m.Client.Download(aud)
		if err != nil {
			return "", mediaType, fmt.Errorf("failed to download audio: %w", err)
		}
		ext := ".ogg"
		contentType := "audio/ogg"
		if aud.GetPtt() {
			contentType = "audio/ogg; codecs=opus"
		}
		filename := fmt.Sprintf("%s%s", messageId, ext)
		url, err := m.Storage.UploadMedia(ctx, data, filename, contentType)
		return url, mediaType, err
	}

	if vid := msg.GetVideoMessage(); vid != nil {
		mediaType = "video"
		data, err := m.Client.Download(vid)
		if err != nil {
			return "", mediaType, fmt.Errorf("failed to download video: %w", err)
		}
		filename := fmt.Sprintf("%s.mp4", messageId)
		url, err := m.Storage.UploadMedia(ctx, data, filename, "video/mp4")
		return url, mediaType, err
	}

	if doc := msg.GetDocumentMessage(); doc != nil {
		mediaType = "document"
		data, err := m.Client.Download(doc)
		if err != nil {
			return "", mediaType, fmt.Errorf("failed to download document: %w", err)
		}
		filename := doc.GetFileName()
		if filename == "" {
			filename = fmt.Sprintf("%s.bin", messageId)
		}
		url, err := m.Storage.UploadMedia(ctx, data, filename, doc.GetMimetype())
		return url, mediaType, err
	}

	if stk := msg.GetStickerMessage(); stk != nil {
		mediaType = "sticker"
		data, err := m.Client.Download(stk)
		if err != nil {
			return "", mediaType, fmt.Errorf("failed to download sticker: %w", err)
		}
		filename := fmt.Sprintf("%s.webp", messageId)
		url, err := m.Storage.UploadMedia(ctx, data, filename, "image/webp")
		return url, mediaType, err
	}

	return "", "text", nil
}

// LogMediaSummary prints media upload summary
func LogMediaSummary(messageId, mediaType, url string) {
	log.Printf("[Media] Uploaded %s for message %s to MinIO: %s", mediaType, messageId, url)
}
