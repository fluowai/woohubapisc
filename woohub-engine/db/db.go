package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"whatsmeow/config"
)

type DBService struct {
	DB *sql.DB
}

func NewDBService(cfg *config.Config) (*DBService, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(10 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Printf("[DB] Warning: PingContext error: %v", err)
	} else {
		log.Printf("[DB] Connected to PostgreSQL successfully")
	}

	return &DBService{DB: db}, nil
}

// Close closes the database connection
func (s *DBService) Close() error {
	if s.DB != nil {
		return s.DB.Close()
	}
	return nil
}

// ResolveAccountId gets the first or default account_id
func (s *DBService) ResolveAccountId(ctx context.Context) (string, error) {
	var accountId string
	// Try to get from active whatsapp_channels or profiles or accounts
	err := s.DB.QueryRowContext(ctx, `
		SELECT account_id FROM public.whatsapp_channels 
		WHERE connection_state = 'connected' AND account_id IS NOT NULL 
		ORDER BY updated_at DESC LIMIT 1
	`).Scan(&accountId)
	if err == nil && accountId != "" {
		return accountId, nil
	}

	err = s.DB.QueryRowContext(ctx, `SELECT id FROM public.accounts ORDER BY created_at ASC LIMIT 1`).Scan(&accountId)
	if err == nil && accountId != "" {
		return accountId, nil
	}

	return "7a29c8c2-7239-4f73-b8a8-3730ae7d6d2d", nil
}

// FindOrCreateContact finds or creates a lead/contact in public.contacts, ensuring MinIO avatar_url is set
func (s *DBService) FindOrCreateContact(ctx context.Context, accountId, phone, name, avatarUrl string, isGroup bool) (string, error) {
	var contactId string
	var existingAvatar string
	var existingName string

	// Build search candidates (with and without 9th digit, with and without +)
	cleanPhone := strings.TrimPrefix(phone, "+")
	candidates := []string{phone, "+" + cleanPhone, cleanPhone}

	if strings.HasPrefix(cleanPhone, "55") && (len(cleanPhone) == 12 || len(cleanPhone) == 13) {
		ddd := cleanPhone[2:4]
		if len(cleanPhone) == 13 && cleanPhone[4] == '9' {
			without9 := "55" + ddd + cleanPhone[5:]
			candidates = append(candidates, without9, "+"+without9)
		} else if len(cleanPhone) == 12 {
			with9 := "55" + ddd + "9" + cleanPhone[4:]
			candidates = append(candidates, with9, "+"+with9)
		}
	}

	// Query existing contact
	query := `
		SELECT id, name, COALESCE(avatar_url, '') 
		FROM public.contacts 
		WHERE account_id = $1 AND phone = ANY($2) 
		ORDER BY updated_at DESC LIMIT 1
	`
	err := s.DB.QueryRowContext(ctx, query, accountId, pqStringArray(candidates)).Scan(&contactId, &existingName, &existingAvatar)

	if err == nil && contactId != "" {
		// Contact exists. Update avatar_url if missing, or update name if pushName is more specific
		shouldUpdate := false
		updateAvatar := existingAvatar
		updateName := existingName

		if existingAvatar == "" && avatarUrl != "" {
			updateAvatar = avatarUrl
			shouldUpdate = true
		}

		if name != "" && !strings.Contains(name, "@") && (existingName == "" || existingName == phone || strings.HasPrefix(existingName, "Contato ")) {
			updateName = name
			shouldUpdate = true
		}

		if shouldUpdate {
			_, _ = s.DB.ExecContext(ctx, `
				UPDATE public.contacts 
				SET name = $1, avatar_url = $2, updated_at = NOW() 
				WHERE id = $3
			`, updateName, updateAvatar, contactId)
		}

		return contactId, nil
	}

	// Create new contact
	company := ""
	if isGroup {
		company = "Grupo WhatsApp"
	}

	// Get an admin user id for assignment
	var userId string
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM public.profiles WHERE account_id = $1 LIMIT 1`, accountId).Scan(&userId)
	if userId == "" {
		userId = "629b94f7-5e2b-470f-b995-ad5f293af073"
	}

	insertQuery := `
		INSERT INTO public.contacts (account_id, user_id, phone, name, avatar_url, company, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NOW(), NOW())
		RETURNING id
	`
	err = s.DB.QueryRowContext(ctx, insertQuery, accountId, userId, phone, name, avatarUrl, company).Scan(&contactId)
	if err != nil {
		// Fallback: search again in case of race condition
		err2 := s.DB.QueryRowContext(ctx, query, accountId, pqStringArray(candidates)).Scan(&contactId, &existingName, &existingAvatar)
		if err2 == nil && contactId != "" {
			return contactId, nil
		}
		return "", fmt.Errorf("failed to insert contact: %w", err)
	}

	return contactId, nil
}

// FindOrCreateConversation finds or creates an active conversation in public.conversations
func (s *DBService) FindOrCreateConversation(ctx context.Context, accountId, contactId string) (string, error) {
	var convId string
	var status string

	err := s.DB.QueryRowContext(ctx, `
		SELECT id, status FROM public.conversations 
		WHERE account_id = $1 AND contact_id = $2 
		LIMIT 1
	`, accountId, contactId).Scan(&convId, &status)

	if err == nil && convId != "" {
		if status == "closed" {
			_, _ = s.DB.ExecContext(ctx, `UPDATE public.conversations SET status = 'open', updated_at = NOW() WHERE id = $1`, convId)
		}
		return convId, nil
	}

	var userId string
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM public.profiles WHERE account_id = $1 LIMIT 1`, accountId).Scan(&userId)
	if userId == "" {
		userId = "629b94f7-5e2b-470f-b995-ad5f293af073"
	}

	insertQuery := `
		INSERT INTO public.conversations (account_id, user_id, contact_id, status, channel, created_at, updated_at)
		VALUES ($1, $2, $3, 'open', 'whatsapp', NOW(), NOW())
		RETURNING id
	`
	err = s.DB.QueryRowContext(ctx, insertQuery, accountId, userId, contactId).Scan(&convId)
	if err != nil {
		// Check again on race
		_ = s.DB.QueryRowContext(ctx, `SELECT id FROM public.conversations WHERE account_id = $1 AND contact_id = $2 LIMIT 1`, accountId, contactId).Scan(&convId)
		if convId != "" {
			return convId, nil
		}
		return "", fmt.Errorf("failed to insert conversation: %w", err)
	}

	return convId, nil
}

// InsertMessage persists incoming or outgoing message into public.messages
func (s *DBService) InsertMessage(ctx context.Context, conversationId, messageId, senderType, contentText, mediaUrl, mediaType string) error {
	query := `
		INSERT INTO public.messages (conversation_id, message_id, sender_type, content_text, media_url, media_type, status, created_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, 'delivered', NOW())
		ON CONFLICT (conversation_id, message_id) DO NOTHING
	`
	_, err := s.DB.ExecContext(ctx, query, conversationId, messageId, senderType, contentText, mediaUrl, mediaType)
	if err != nil {
		return fmt.Errorf("failed to insert message: %w", err)
	}

	// Update conversation metadata
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE public.conversations 
		SET last_message_text = $1, last_message_at = NOW(), updated_at = NOW() 
		WHERE id = $2
	`, contentText, conversationId)

	return nil
}

// UpsertConnectedChannel registers or updates the connected WhatsApp channel
func (s *DBService) UpsertConnectedChannel(ctx context.Context, accountId, phone, name string) error {
	cleanPhone := FormatE164(phone)
	channelName := name
	if channelName == "" {
		channelName = fmt.Sprintf("WhatsApp (%s)", cleanPhone)
	}

	query := `
		INSERT INTO public.whatsapp_channels (account_id, provider, name, phone_number_id, connection_state, updated_at, created_at)
		VALUES ($1, 'whatsmeow', $2, $3, 'connected', NOW(), NOW())
		ON CONFLICT (phone_number_id) DO UPDATE 
		SET provider = 'whatsmeow', name = EXCLUDED.name, connection_state = 'connected', updated_at = NOW()
	`
	_, err := s.DB.ExecContext(ctx, query, accountId, channelName, cleanPhone)
	if err != nil {
		// If check constraint does not permit whatsmeow yet, fallback to 'woohub' while migration runs
		if strings.Contains(err.Error(), "check") {
			_, err = s.DB.ExecContext(ctx, `
				INSERT INTO public.whatsapp_channels (account_id, provider, name, phone_number_id, connection_state, updated_at, created_at)
				VALUES ($1, 'woohub', $2, $3, 'connected', NOW(), NOW())
				ON CONFLICT (phone_number_id) DO UPDATE 
				SET provider = 'woohub', name = EXCLUDED.name, connection_state = 'connected', updated_at = NOW()
			`, accountId, channelName, cleanPhone)
		}
	}
	return err
}

func pqStringArray(arr []string) string {
	var escaped []string
	for _, s := range arr {
		escaped = append(escaped, fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `\"`)))
	}
	return "{" + strings.Join(escaped, ",") + "}"
}
