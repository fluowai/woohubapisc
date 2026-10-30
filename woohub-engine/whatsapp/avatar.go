package whatsapp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// FetchAndStoreLeadAvatar retrieves the WhatsApp profile picture for a JID and saves it permanently in MinIO
func (m *WhatsAppManager) FetchAndStoreLeadAvatar(ctx context.Context, jid types.JID, phone string) (string, error) {
	if m.Client == nil || !m.Client.IsConnected() {
		return "", fmt.Errorf("whatsapp client not connected")
	}

	cleanPhone := strings.TrimPrefix(phone, "+")
	if cleanPhone == "" {
		cleanPhone = jid.User
	}

	// Fetch profile picture from WhatsApp CDN
	ctxTimeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	picInfo, err := m.Client.GetProfilePictureInfo(jid, &whatsmeow.GetProfilePictureParams{
		Preview: false,
	})
	if err != nil {
		// Many users have privacy settings hiding profile pictures (or no picture set)
		return "", err
	}

	if picInfo == nil || picInfo.URL == "" {
		return "", nil
	}

	// Download from WhatsApp CDN and upload directly to MinIO bucket woochat/avatars/<phone>.jpg
	minioUrl, err := m.Storage.DownloadAndUploadAvatar(ctxTimeout, picInfo.URL, cleanPhone)
	if err != nil {
		log.Printf("[Avatar] Failed to upload avatar for %s to MinIO: %v", cleanPhone, err)
		return picInfo.URL, nil // Fallback to WhatsApp CDN URL if MinIO fails
	}

	log.Printf("[Avatar] Saved lead avatar in MinIO: %s", minioUrl)
	return minioUrl, nil
}
