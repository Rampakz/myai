package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	qrcode "github.com/skip2/go-qrcode"
	"wa-blast/internal/ai"
	"wa-blast/internal/db"
	"wa-blast/internal/whatsapp"
)

type Server struct {
	db  *db.DB
	wa  *whatsapp.Client
	ai  *ai.Client
	mux *mux.Router
}

func NewServer(database *db.DB, waClient *whatsapp.Client, aiClient *ai.Client) *Server {
	s := &Server{db: database, wa: waClient, ai: aiClient}
	s.mux = mux.NewRouter()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.PathPrefix("/web/").Handler(http.StripPrefix("/web/", http.FileServer(http.Dir("web"))))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/index.html")
	})

	api := s.mux.PathPrefix("/api").Subrouter()
	api.Use(jsonMiddleware)

	// Dashboard
	api.HandleFunc("/stats", s.getStats).Methods("GET")

	// Inbox
	api.HandleFunc("/inbox", s.listInbox).Methods("GET")
	api.HandleFunc("/inbox/read", s.markRead).Methods("POST")

	// WhatsApp status
	api.HandleFunc("/whatsapp/status", s.waStatus).Methods("GET")
	api.HandleFunc("/whatsapp/qr.png", s.waQRPNG).Methods("GET")

	// Contacts
	api.HandleFunc("/contacts", s.listContacts).Methods("GET")
	api.HandleFunc("/contacts", s.createContact).Methods("POST")
	api.HandleFunc("/contacts/import", s.importContacts).Methods("POST")
	api.HandleFunc("/contacts/overdue", s.overdueContacts).Methods("GET")
	api.HandleFunc("/contacts/{id}/visit", s.markVisit).Methods("POST")
	api.HandleFunc("/contacts/{id}", s.deleteContact).Methods("DELETE")

	// Templates
	api.HandleFunc("/templates", s.listTemplates).Methods("GET")
	api.HandleFunc("/templates", s.createTemplate).Methods("POST")
	api.HandleFunc("/templates/{id}", s.updateTemplate).Methods("PUT")
	api.HandleFunc("/templates/{id}", s.deleteTemplate).Methods("DELETE")

	// Broadcasts
	api.HandleFunc("/broadcasts", s.listBroadcasts).Methods("GET")
	api.HandleFunc("/broadcasts", s.createBroadcast).Methods("POST")
	api.HandleFunc("/broadcasts/{id}/send", s.sendBroadcast).Methods("POST")
	api.HandleFunc("/broadcasts/{id}/logs", s.broadcastLogs).Methods("GET")
	api.HandleFunc("/broadcasts/{id}", s.deleteBroadcast).Methods("DELETE")

	// AI
	api.HandleFunc("/ai/generate", s.aiGenerate).Methods("POST")
	api.HandleFunc("/ai/analyze", s.aiAnalyze).Methods("GET")
	api.HandleFunc("/ai/status", s.aiStatus).Methods("GET")
}

func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, code int, data interface{}) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, code int, msg string) {
	respond(w, code, map[string]string{"error": msg})
}

// Inbox

func (s *Server) listInbox(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.db.ListIncoming(100)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if msgs == nil {
		msgs = []db.IncomingMessage{}
	}
	respond(w, 200, map[string]interface{}{
		"messages": msgs,
		"unread":   s.db.UnreadCount(),
	})
}

func (s *Server) markRead(w http.ResponseWriter, r *http.Request) {
	s.db.MarkAllRead()
	respond(w, 200, map[string]bool{"ok": true})
}

// Dashboard

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.GetStats()
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	weekly, _ := s.db.GetWeeklyVisits()
	top, _ := s.db.GetTopClients(5)
	respond(w, 200, map[string]interface{}{
		"stats":  stats,
		"weekly": weekly,
		"top":    top,
	})
}

// WhatsApp

func (s *Server) waStatus(w http.ResponseWriter, r *http.Request) {
	status, qrCode := s.wa.Status()
	respond(w, 200, map[string]string{"status": status, "qr": qrCode})
}

func (s *Server) waQRPNG(w http.ResponseWriter, r *http.Request) {
	_, qrCode := s.wa.Status()
	if qrCode == "" {
		http.Error(w, "no QR", http.StatusNoContent)
		return
	}
	png, err := qrcode.Encode(qrCode, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// Contacts

func (s *Server) listContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.db.ListContacts()
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if contacts == nil {
		contacts = []db.Contact{}
	}
	respond(w, 200, contacts)
}

func (s *Server) createContact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, 400, "invalid json")
		return
	}
	if body.Phone == "" {
		respondError(w, 400, "phone is required")
		return
	}
	c, err := s.db.CreateContact(body.Phone, body.Name)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 201, c)
}

func (s *Server) importContacts(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		respondError(w, 400, "file required")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		respondError(w, 400, "invalid csv: "+err.Error())
		return
	}

	var contacts []db.Contact
	for i, row := range records {
		if i == 0 && len(row) > 0 && strings.ToLower(row[0]) == "phone" {
			continue // skip header
		}
		if len(row) == 0 {
			continue
		}
		c := db.Contact{Phone: strings.TrimSpace(row[0])}
		if len(row) > 1 {
			c.Name = strings.TrimSpace(row[1])
		}
		contacts = append(contacts, c)
	}

	count, err := s.db.ImportContacts(contacts)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]int{"imported": count})
}

func (s *Server) markVisit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	if err := s.db.MarkVisit(id); err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (s *Server) overdueContacts(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days := 21
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
			days = d
		}
	}
	contacts, err := s.db.ListContactsNeedingVisit(days)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if contacts == nil {
		contacts = []db.Contact{}
	}
	respond(w, 200, map[string]interface{}{"contacts": contacts, "days": days})
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	if err := s.db.DeleteContact(id); err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

// Templates

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.db.ListTemplates()
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if templates == nil {
		templates = []db.Template{}
	}
	respond(w, 200, templates)
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, 400, "invalid json")
		return
	}
	if body.Name == "" || body.Body == "" {
		respondError(w, 400, "name and body are required")
		return
	}
	t, err := s.db.CreateTemplate(body.Name, body.Body)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 201, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	var body struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, 400, "invalid json")
		return
	}
	if err := s.db.UpdateTemplate(id, body.Name, body.Body); err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	if err := s.db.DeleteTemplate(id); err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

// Broadcasts

func (s *Server) listBroadcasts(w http.ResponseWriter, r *http.Request) {
	broadcasts, err := s.db.ListBroadcasts()
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if broadcasts == nil {
		broadcasts = []db.Broadcast{}
	}
	respond(w, 200, broadcasts)
}

func (s *Server) createBroadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string  `json:"name"`
		TemplateID int64   `json:"template_id"`
		Schedule   string  `json:"schedule"`
		ContactIDs []int64 `json:"contact_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, 400, "invalid json")
		return
	}
	if body.Name == "" || body.TemplateID == 0 {
		respondError(w, 400, "name and template_id are required")
		return
	}
	b, err := s.db.CreateBroadcast(body.Name, body.TemplateID, body.Schedule, body.ContactIDs)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 201, b)
}

func (s *Server) sendBroadcast(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	if !s.wa.IsReady() {
		respondError(w, 503, "WhatsApp not connected")
		return
	}
	b, err := s.db.GetBroadcast(id)
	if err != nil {
		respondError(w, 404, "broadcast not found")
		return
	}
	tmpl, err := s.db.GetTemplate(b.TemplateID)
	if err != nil {
		respondError(w, 500, "template not found")
		return
	}
	contacts, err := s.db.GetBroadcastContacts(id)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}

	go func() {
		s.db.UpdateBroadcastStatus(id, "running")
		for _, c := range contacts {
			text := whatsapp.RenderTemplate(tmpl.Body, c.Name, c.Phone)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := s.wa.SendText(ctx, c.Phone, text)
			cancel()
			status := "sent"
			errMsg := ""
			if err != nil {
				status = "failed"
				errMsg = err.Error()
			}
			s.db.AddLog(id, c.ID, c.Phone, c.Name, status, errMsg)
			time.Sleep(2 * time.Second) // avoid spam detection
		}
		s.db.UpdateBroadcastStatus(id, "done")
	}()

	respond(w, 200, map[string]string{"status": "sending"})
}

func (s *Server) broadcastLogs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	logs, err := s.db.GetBroadcastLogs(id)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if logs == nil {
		logs = []db.BroadcastLog{}
	}
	sent, failed, _ := s.db.GetBroadcastStats(id)
	respond(w, 200, map[string]interface{}{
		"logs":   logs,
		"sent":   sent,
		"failed": failed,
	})
}

func (s *Server) deleteBroadcast(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil {
		respondError(w, 400, "invalid id")
		return
	}
	if err := s.db.DeleteBroadcast(id); err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

// AI

func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, map[string]bool{"enabled": s.ai != nil})
}

func (s *Server) aiGenerate(w http.ResponseWriter, r *http.Request) {
	if s.ai == nil {
		respondError(w, 503, "AI not configured")
		return
	}
	var body struct {
		Description string `json:"description"`
		ClientName  string `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Description == "" {
		respondError(w, 400, "description is required")
		return
	}
	text, err := s.ai.GenerateBroadcast(r.Context(), body.Description, body.ClientName)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	respond(w, 200, map[string]string{"text": text})
}

func (s *Server) aiAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.ai == nil {
		respondError(w, 503, "AI not configured")
		return
	}
	daysStr := r.URL.Query().Get("days")
	days := 21
	if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
		days = d
	}
	contacts, err := s.db.ListContactsNeedingVisit(days)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}

	var clientInfos []ai.ClientInfo
	for _, c := range contacts {
		days := 0
		if c.DaysSinceVisit != nil {
			days = *c.DaysSinceVisit
		}
		clientInfos = append(clientInfos, ai.ClientInfo{
			Phone:     c.Phone,
			Name:      c.Name,
			DaysSince: days,
		})
	}

	insights, err := s.ai.AnalyzeClients(r.Context(), clientInfos)
	if err != nil {
		respondError(w, 500, err.Error())
		return
	}
	if insights == nil {
		insights = []ai.ClientInsight{}
	}
	respond(w, 200, map[string]interface{}{"insights": insights})
}
