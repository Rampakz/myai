package whatsapp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// MessageHandler is called for every incoming message
type MessageHandler func(phone, name, text string)

type Client struct {
	wac            *whatsmeow.Client
	mu             sync.RWMutex
	qrCode         string
	status         string
	dbPath         string
	container      *sqlstore.Container
	onMessage      MessageHandler
}

func (c *Client) OnMessage(h MessageHandler) {
	c.onMessage = h
}

func New(dbPath string) (*Client, error) {
	ctx := context.Background()
	logger := waLog.Stdout("WA", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on", logger)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: %w", err)
	}
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}
	clientLog := waLog.Stdout("Client", "WARN", true)
	wac := whatsmeow.NewClient(deviceStore, clientLog)

	c := &Client{
		wac:       wac,
		status:    "disconnected",
		dbPath:    dbPath,
		container: container,
	}
	return c, nil
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	c.status = "connecting"
	c.mu.Unlock()

	// Handle all events
	c.wac.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			if v.Info.IsGroup {
				return
			}
			// Detect self-message (Saved Messages): sender == chat, both same entity
			isSavedMessages := v.Info.IsFromMe && v.Info.Chat.User == v.Info.Sender.User
			if v.Info.IsFromMe && !isSavedMessages {
				return
			}
			text := ""
			if v.Message.GetConversation() != "" {
				text = v.Message.GetConversation()
			} else if v.Message.GetExtendedTextMessage() != nil {
				text = v.Message.GetExtendedTextMessage().GetText()
			}
			if text == "" {
				return
			}
			// Encode full JID so replies go to the correct server (lid vs s.whatsapp.net)
			phone := v.Info.Sender.User + "@" + v.Info.Sender.Server
			if isSavedMessages {
				phone = "__self__"
			}
			if c.onMessage != nil {
				c.onMessage(phone, "", text)
			}
		case *events.Disconnected:
			c.mu.Lock()
			c.status = "disconnected"
			c.mu.Unlock()
			log.Println("WhatsApp disconnected, reconnecting in 5s...")
			time.Sleep(5 * time.Second)
			c.reconnect()
		case *events.Connected:
			c.mu.Lock()
			c.qrCode = ""
			c.status = "connected"
			c.mu.Unlock()
			log.Println("WhatsApp connected")
		case *events.LoggedOut:
			c.mu.Lock()
			c.status = "disconnected"
			c.qrCode = ""
			c.mu.Unlock()
			log.Println("WhatsApp logged out")
		}
	})

	return c.doConnect(ctx)
}

func (c *Client) doConnect(ctx context.Context) error {
	if c.wac.Store.ID == nil {
		qrChan, _ := c.wac.GetQRChannel(ctx)
		if err := c.wac.Connect(); err != nil {
			c.mu.Lock()
			c.status = "disconnected"
			c.mu.Unlock()
			return err
		}
		go func() {
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					c.mu.Lock()
					c.qrCode = evt.Code
					c.status = "qr_pending"
					c.mu.Unlock()
				case "success":
					c.mu.Lock()
					c.qrCode = ""
					c.status = "connected"
					c.mu.Unlock()
				case "timeout":
					c.mu.Lock()
					c.qrCode = ""
					c.status = "qr_timeout"
					c.mu.Unlock()
					log.Println("QR code expired, will generate new one on next status check")
				}
			}
		}()
	} else {
		if err := c.wac.Connect(); err != nil {
			c.mu.Lock()
			c.status = "disconnected"
			c.mu.Unlock()
			return err
		}
	}

	// Wait up to 10s for initial state
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		s := c.status
		c.mu.RUnlock()
		if s == "connected" || s == "qr_pending" {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

func (c *Client) reconnect() {
	if c.wac.IsConnected() {
		return
	}
	log.Println("Attempting WhatsApp reconnect...")
	if err := c.wac.Connect(); err != nil {
		log.Printf("Reconnect failed: %v, retrying in 30s", err)
		time.Sleep(30 * time.Second)
		go c.reconnect()
	}
}

// RefreshQR generates a fresh QR when the previous one expired
func (c *Client) RefreshQR(ctx context.Context) {
	c.mu.RLock()
	s := c.status
	c.mu.RUnlock()
	if s != "qr_timeout" && s != "disconnected" {
		return
	}
	c.wac.Disconnect()
	time.Sleep(time.Second)
	go c.doConnect(ctx)
}

func (c *Client) Status() (status, qrCode string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.wac.IsConnected() && c.wac.IsLoggedIn() {
		return "connected", ""
	}
	return c.status, c.qrCode
}

func (c *Client) IsReady() bool {
	return c.wac.IsConnected() && c.wac.IsLoggedIn()
}

func (c *Client) SendText(ctx context.Context, phone, text string) error {
	if !c.IsReady() {
		return fmt.Errorf("WhatsApp не подключён")
	}
	var jid types.JID
	if strings.Contains(phone, "@") {
		parts := strings.SplitN(phone, "@", 2)
		jid = types.NewJID(parts[0], parts[1])
	} else {
		jid = types.NewJID(sanitizePhone(phone), types.DefaultUserServer)
	}
	_, err := c.wac.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	return err
}

func RenderTemplate(body, name, phone string) string {
	r := strings.NewReplacer(
		"{{name}}", name,
		"{{phone}}", phone,
		"{{Name}}", name,
		"{{Phone}}", phone,
	)
	return r.Replace(body)
}

func sanitizePhone(phone string) string {
	phone = strings.TrimPrefix(phone, "+")
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	return phone
}
