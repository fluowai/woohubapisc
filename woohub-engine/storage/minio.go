package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"whatsmeow/config"
)

type MinioService struct {
	client     *minio.Client
	bucket     string
	publicBase string
}

func NewMinioService(cfg *config.Config) (*MinioService, error) {
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
		Region: cfg.MinioRegion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MinIO client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ensure bucket exists
	exists, err := client.BucketExists(ctx, cfg.MinioBucket)
	if err != nil {
		log.Printf("[MinIO] Warning: BucketExists check error: %v", err)
	} else if !exists {
		err = client.MakeBucket(ctx, cfg.MinioBucket, minio.MakeBucketOptions{Region: cfg.MinioRegion})
		if err != nil {
			log.Printf("[MinIO] Warning: MakeBucket error: %v", err)
		} else {
			log.Printf("[MinIO] Created bucket %s", cfg.MinioBucket)
		}
	} else {
		log.Printf("[MinIO] Bucket %s is ready", cfg.MinioBucket)
	}

	return &MinioService{
		client:     client,
		bucket:     cfg.MinioBucket,
		publicBase: cfg.MinioPublicBaseURL,
	}, nil
}

// UploadMedia uploads binary media (image, audio, video, document, sticker) to MinIO and returns the public URL
func (s *MinioService) UploadMedia(ctx context.Context, data []byte, filename, contentType string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty media data")
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = mimeToExt(contentType)
	}

	cleanBase := strings.TrimSuffix(filepath.Base(filename), ext)
	if cleanBase == "" || cleanBase == "." {
		cleanBase = "media"
	}

	objectKey := fmt.Sprintf("media/%s_%s%s", uuid.New().String()[:8], cleanBase, ext)

	if contentType == "" {
		contentType = extToMime(ext)
	}

	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to put object to MinIO: %w", err)
	}

	publicUrl := fmt.Sprintf("%s/%s/%s", s.publicBase, s.bucket, objectKey)
	return publicUrl, nil
}

// UploadAvatar uploads a contact profile picture / avatar to MinIO and returns the public URL
func (s *MinioService) UploadAvatar(ctx context.Context, data []byte, phone string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty avatar data")
	}

	cleanPhone := strings.TrimPrefix(phone, "+")
	cleanPhone = strings.ReplaceAll(cleanPhone, "@s.whatsapp.net", "")
	cleanPhone = strings.ReplaceAll(cleanPhone, "@g.us", "")

	objectKey := fmt.Sprintf("avatars/%s.jpg", cleanPhone)

	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: "image/jpeg",
	})
	if err != nil {
		return "", fmt.Errorf("failed to put avatar to MinIO: %w", err)
	}

	publicUrl := fmt.Sprintf("%s/%s/%s", s.publicBase, s.bucket, objectKey)
	return publicUrl, nil
}

// DownloadAndUploadAvatar fetches an avatar from an external URL and uploads it directly to MinIO
func (s *MinioService) DownloadAndUploadAvatar(ctx context.Context, externalUrl, phone string) (string, error) {
	if externalUrl == "" {
		return "", fmt.Errorf("empty external url")
	}

	// If already in our MinIO bucket, return as is
	if strings.Contains(externalUrl, s.bucket) && strings.Contains(externalUrl, s.publicBase) {
		return externalUrl, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", externalUrl, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download avatar, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return s.UploadAvatar(ctx, body, phone)
}

func mimeToExt(mime string) string {
	mime = strings.ToLower(strings.Split(mime, ";")[0])
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/m4a":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/msword":
		return ".doc"
	default:
		return ".bin"
	}
}

func extToMime(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".mp4":
		return "video/mp4"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".doc":
		return "application/msword"
	default:
		return "application/octet-stream"
	}
}
