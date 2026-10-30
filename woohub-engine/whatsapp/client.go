package whatsapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"whatsmeow/config"
	"whatsmeow/db"
	"whatsmeow/storage"
)

type WhatsAppManager struct {
	Client    *whatsmeow.Client
	Container *sqlstore.Container
	Cfg       *config.Config
	Storage   *storage.MinioService
	DB        *db.DBService

	mu          sync.RWMutex
	currentQR   string
	qrDataURL   string
	status      string // "connected", "scan_qr", "disconnected", "connecting"
	activePhone string
}

func NewWhatsAppManager(cfg *config.Config, minioService *storage.MinioService, dbService *db.DBService) (*WhatsAppManager, error) {
	dbLogger := waLog.Stdout("Database", "WARN", true)

	container, err := sqlstore.New("postgres", cfg.DatabaseURL, dbLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sqlstore: %w", err)
	}

	mgr := &WhatsAppManager{
		Container: container,
		Cfg:       cfg,
		Storage:   minioService,
		DB:        dbService,
		status:    "disconnected",
	}

	return mgr, nil
}

// StartSession initializes the device and client connection
func (m *WhatsAppManager) StartSession(ctx context.Context) error {
	m.mu.Lock()
	if m.Client != nil && m.Client.IsConnected() {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// Get or create device
	deviceStore, err := m.Container.GetFirstDevice()
	if err != nil {
		return fmt.Errorf("failed to get first device: %w", err)
	}

	if deviceStore == nil {
		deviceStore = m.Container.NewDevice()
		log.Println("[Whatsmeow] New device registered in sqlstore")
	}

	clientLog := waLog.Stdout("Whatsmeow", "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	client.AddEventHandler(m.EventHandler)

	m.mu.Lock()
	m.Client = client
	m.mu.Unlock()

	if client.Store.ID == nil {
		// Device is not paired yet. Generate QR code
		m.mu.Lock()
		m.status = "scan_qr"
		m.mu.Unlock()

		qrChan, err := client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("failed to get QR channel: %w", err)
		}

		err = client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		go m.handleQRStream(qrChan)
	} else {
		// Already paired device
		phone := client.Store.ID.User
		m.mu.Lock()
		m.activePhone = FormatE164(phone)
		m.status = "connecting"
		m.mu.Unlock()

		err = client.Connect()
		if err != nil {
			log.Printf("[Whatsmeow] Connect error: %v", err)
			return err
		}

		m.mu.Lock()
		m.status = "connected"
		m.mu.Unlock()

		log.Printf("[Whatsmeow] Reconnected session with phone: %s", m.activePhone)

		// Sync with database whatsapp_channels
		go func() {
			acctId, _ := m.DB.ResolveAccountId(context.Background())
			_ = m.DB.UpsertConnectedChannel(context.Background(), acctId, m.activePhone, fmt.Sprintf("WhatsApp (%s)", m.activePhone))
		}()
	}

	return nil
}

func (m *WhatsAppManager) handleQRStream(qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		switch item.Event {
		case "code":
			log.Println("[Whatsmeow] New QR Code received from WhatsApp")
			pngBytes, err := qrcode.Encode(item.Code, qrcode.Medium, 256)
			var dataURL string
			if err == nil {
				dataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
			}

			m.mu.Lock()
			m.currentQR = item.Code
			m.qrDataURL = dataURL
			m.status = "scan_qr"
			m.mu.Unlock()

		case "success":
			log.Println("[Whatsmeow] Successfully paired QR code!")
			m.mu.Lock()
			m.currentQR = ""
			m.qrDataURL = ""
			m.status = "connected"
			if m.Client != nil && m.Client.Store.ID != nil {
				m.activePhone = FormatE164(m.Client.Store.ID.User)
			}
			m.mu.Unlock()

			// Sync with DB
			go func() {
				acctId, _ := m.DB.ResolveAccountId(context.Background())
				_ = m.DB.UpsertConnectedChannel(context.Background(), acctId, m.activePhone, fmt.Sprintf("WhatsApp (%s)", m.activePhone))
			}()

		case "timeout":
			log.Println("[Whatsmeow] QR code scan timed out")
			m.mu.Lock()
			if m.status != "connected" {
				m.status = "scan_qr"
			}
			m.mu.Unlock()

		default:
			log.Printf("[Whatsmeow] QR event: %s", item.Event)
		}
	}
}

// GetStatus returns the current connection status, active phone, and QR code data URL
func (m *WhatsAppManager) GetStatus() (status, phone, qrDataURL, rawQR string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status = m.status
	if m.Client != nil && m.Client.IsConnected() {
		status = "connected"
	} else if status == "connected" {
		status = "disconnected"
	}

	return status, m.activePhone, m.qrDataURL, m.currentQR
}

// Logout unpairs the session and clears credentials
func (m *WhatsAppManager) Logout(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Client != nil {
		if m.Client.IsConnected() {
			_ = m.Client.Logout()
		}
		m.Client.Disconnect()
	}

	m.status = "disconnected"
	m.activePhone = ""
	m.currentQR = ""
	m.qrDataURL = ""

	return nil
}
