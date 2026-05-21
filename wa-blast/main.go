package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wa-blast/internal/ai"
	"wa-blast/internal/api"
	"wa-blast/internal/db"
	"wa-blast/internal/scheduler"
	"wa-blast/internal/whatsapp"
)

// detectAction classifies incoming message intent
func detectAction(text string) string {
	t := strings.ToLower(text)
	visitWords := []string{"да", "ок", "окей", "ok", "приду", "буду", "хорошо", "договорились", "жди", "иду"}
	bookingWords := []string{"запиши", "записаться", "хочу записаться", "когда можно", "свободно", "запись", "занят"}
	for _, w := range visitWords {
		if strings.Contains(t, w) {
			return "visit"
		}
	}
	for _, w := range bookingWords {
		if strings.Contains(t, w) {
			return "booking"
		}
	}
	return ""
}

// handleOwnerCommand processes admin commands sent from the owner's phone.
// Returns true if the message was a command (so normal processing is skipped).
func handleOwnerCommand(database *db.DB, waClient *whatsapp.Client, ownerPhone, text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))

	send := func(msg string) {
		if err := waClient.SendText(context.Background(), ownerPhone, msg); err != nil {
			log.Printf("[OWNER] send error: %v", err)
		}
	}

	switch {
	case t == "помощь" || t == "help" || t == "/help":
		send("Команды барбера 💈\n\n" +
			"*список* — кто не был 21+ дней\n" +
			"*сегодня* — визиты сегодня\n" +
			"*стата* — статистика\n" +
			"*напомни [имя]* — напомнить клиенту\n" +
			"*помощь* — это меню")
		return true

	case t == "список" || t == "клиенты":
		contacts, _ := database.ListContactsNeedingVisit(21)
		if len(contacts) == 0 {
			send("Все клиенты были недавно 🎉")
			return true
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Не были 21+ дней (%d чел.):\n\n", len(contacts))
		for i, c := range contacts {
			if i >= 10 {
				fmt.Fprintf(&sb, "...и ещё %d", len(contacts)-10)
				break
			}
			name := c.Name
			if name == "" {
				name = c.Phone
			}
			days := 0
			if c.DaysSinceVisit != nil {
				days = *c.DaysSinceVisit
			}
			fmt.Fprintf(&sb, "• %s — %d дн.\n", name, days)
		}
		send(sb.String())
		return true

	case t == "сегодня":
		contacts, _ := database.GetTodayVisits()
		if len(contacts) == 0 {
			send("Сегодня визитов нет")
			return true
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Сегодня были (%d чел.):\n\n", len(contacts))
		for _, c := range contacts {
			name := c.Name
			if name == "" {
				name = c.Phone
			}
			fmt.Fprintf(&sb, "✓ %s\n", name)
		}
		send(sb.String())
		return true

	case t == "стата" || t == "статистика":
		s, err := database.GetStats()
		if err != nil {
			send("Ошибка получения статистики")
			return true
		}
		send(fmt.Sprintf("📊 Статистика\n\n"+
			"Клиентов: %d\n"+
			"Визитов за месяц: %d\n"+
			"Визитов за неделю: %d\n"+
			"Нужно напомнить: %d\n"+
			"Сообщений отправлено: %d",
			s.TotalClients, s.VisitsThisMonth, s.VisitsThisWeek, s.NeedReminder, s.MessagesSentTotal))
		return true

	case strings.HasPrefix(t, "напомни "):
		query := strings.TrimSpace(strings.TrimPrefix(t, "напомни "))
		contact, err := database.FindContactByName(query)
		if err != nil {
			send("Клиент не найден: " + query)
			return true
		}
		templates, _ := database.ListTemplates()
		if len(templates) == 0 {
			send("Нет шаблонов для отправки")
			return true
		}
		tmpl := templates[len(templates)-1] // первый (старейший) шаблон = "Пора подстричься"
		msgText := whatsapp.RenderTemplate(tmpl.Body, contact.Name, contact.Phone)
		if err := waClient.SendText(context.Background(), contact.Phone, msgText); err != nil {
			send("Ошибка отправки: " + err.Error())
			return true
		}
		name := contact.Name
		if name == "" {
			name = contact.Phone
		}
		send(fmt.Sprintf("✅ Напоминание отправлено %s (%s)", name, contact.Phone))
		return true
	}

	return false
}

func findContactID(database *db.DB, phone string) int64 {
	contacts, _ := database.ListContacts()
	for _, c := range contacts {
		if c.Phone == phone {
			return c.ID
		}
	}
	return 0
}

func seedTemplates(database *db.DB) {
	templates, _ := database.ListTemplates()
	if len(templates) > 0 {
		return
	}
	defaults := []struct{ name, body string }{
		{
			"Пора подстричься",
			"Привет, {{name}}! 💈 Прошло уже несколько недель — самое время освежить стрижку. Запишись, жду тебя!",
		},
		{
			"Акция / скидка",
			"Привет, {{name}}! 🎉 Специально для тебя — скидка 15% на следующую стрижку. Действует до конца недели. Пиши, запишу!",
		},
		{
			"Напоминание о записи",
			"Привет, {{name}}! Напоминаю о твоей записи завтра. Если что-то изменилось — напиши, перенесём 🙌",
		},
		{
			"Новая услуга",
			"Привет, {{name}}! В барбершопе появилась новая услуга — уход за бородой. Приходи попробовать! 🧔",
		},
	}
	for _, t := range defaults {
		database.CreateTemplate(t.name, t.body)
	}
	log.Println("Барберские шаблоны созданы")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "."
	}

	database, err := db.New(dataDir + "/wa-blast-data.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	waClient, err := whatsapp.New(dataDir + "/wa-blast-devices.db")
	if err != nil {
		log.Fatalf("failed to create WhatsApp client: %v", err)
	}

	seedTemplates(database)

	// Initialize AI client (optional — works without GEMINI_API_KEY)
	var aiClient *ai.Client
	if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
		var err error
		aiClient, err = ai.New(geminiKey)
		if err != nil {
			log.Printf("AI init error: %v", err)
		} else {
			log.Println("Gemini AI enabled")
			defer aiClient.Close()
		}
	} else {
		log.Println("GEMINI_API_KEY not set — AI features disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ownerPhone := os.Getenv("OWNER_PHONE")
	if ownerPhone != "" {
		log.Printf("Owner phone set: %s", ownerPhone)
	}

	// Handle incoming WhatsApp messages
	waClient.OnMessage(func(phone, _, text string) {
		log.Printf("[DEBUG] msg from=%s text=%q", phone, text)
		// Self-message (Saved Messages) = owner command
		if phone == "__self__" {
			log.Printf("[OWNER] self-message command: %s", text)
			if ownerPhone != "" {
				handleOwnerCommand(database, waClient, ownerPhone, text)
			}
			return
		}
		// Regular message from known owner number (phone may be "user@server")
		phoneUser := strings.SplitN(phone, "@", 2)[0]
		if ownerPhone != "" && phoneUser == ownerPhone {
			log.Printf("[OWNER] command from owner: %s", text)
			if handleOwnerCommand(database, waClient, phone, text) {
				return
			}
		}

		name := database.ContactNameByPhone(phone)
		action := detectAction(text)

		if action == "visit" {
			database.MarkVisit(findContactID(database, phone))
		}

		database.SaveIncoming(phone, name, text, action)
		log.Printf("Incoming from %s (%s): %s [action:%s]", name, phone, text, action)

		if action == "booking" {
			reply := "Отлично! Запишу тебя. Когда удобно? 📅"
			if name != "" {
				reply = name + ", отлично! Запишу тебя. Когда удобно? 📅"
			}
			waClient.SendText(context.Background(), phone, reply)
		} else if action == "" && aiClient != nil {
			go func() {
				reply, err := aiClient.ReplyToClient(context.Background(), name, phone, text)
				if err != nil {
					log.Printf("AI reply error: %v", err)
					return
				}
				if reply != "" {
					waClient.SendText(context.Background(), phone, reply)
				}
			}()
		}
	})

	log.Println("Connecting to WhatsApp...")
	if err := waClient.Connect(ctx); err != nil {
		log.Printf("WhatsApp connect error: %v", err)
	}

	sched := scheduler.New(database, waClient)
	sched.Start()
	defer sched.Stop()

	srv := api.NewServer(database, waClient, aiClient)

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: srv,
	}

	go func() {
		log.Printf("Server running at http://localhost:%s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	httpServer.Shutdown(shutCtx)
}
