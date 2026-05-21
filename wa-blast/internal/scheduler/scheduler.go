package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"wa-blast/internal/db"
	"wa-blast/internal/whatsapp"
)

type Scheduler struct {
	cron *cron.Cron
	db   *db.DB
	wa   *whatsapp.Client
}

func New(database *db.DB, waClient *whatsapp.Client) *Scheduler {
	return &Scheduler{
		cron: cron.New(),
		db:   database,
		wa:   waClient,
	}
}

func (s *Scheduler) Start() {
	s.loadBroadcasts()
	s.cron.Start()
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) loadBroadcasts() {
	broadcasts, err := s.db.GetScheduledBroadcasts()
	if err != nil {
		log.Printf("scheduler: failed to load broadcasts: %v", err)
		return
	}
	for _, b := range broadcasts {
		b := b
		_, err := s.cron.AddFunc(b.Schedule, func() {
			s.runBroadcast(b.ID)
		})
		if err != nil {
			log.Printf("scheduler: invalid cron expression for broadcast %d (%q): %v", b.ID, b.Schedule, err)
		}
	}
}

func (s *Scheduler) AddBroadcast(id int64, schedule string) error {
	_, err := s.cron.AddFunc(schedule, func() {
		s.runBroadcast(id)
	})
	return err
}

func (s *Scheduler) runBroadcast(broadcastID int64) {
	if !s.wa.IsReady() {
		log.Printf("scheduler: WhatsApp not ready, skipping broadcast %d", broadcastID)
		return
	}
	b, err := s.db.GetBroadcast(broadcastID)
	if err != nil {
		log.Printf("scheduler: broadcast %d not found: %v", broadcastID, err)
		return
	}
	tmpl, err := s.db.GetTemplate(b.TemplateID)
	if err != nil {
		log.Printf("scheduler: template not found for broadcast %d: %v", broadcastID, err)
		return
	}
	contacts, err := s.db.GetBroadcastContacts(broadcastID)
	if err != nil {
		log.Printf("scheduler: contacts error for broadcast %d: %v", broadcastID, err)
		return
	}

	s.db.UpdateBroadcastStatus(broadcastID, "running")
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
		s.db.AddLog(broadcastID, c.ID, c.Phone, c.Name, status, errMsg)
		time.Sleep(2 * time.Second)
	}
	s.db.UpdateBroadcastStatus(broadcastID, "done")
	log.Printf("scheduler: broadcast %d done", broadcastID)
}
