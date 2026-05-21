package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
}

type Contact struct {
	ID            int64      `json:"id"`
	Phone         string     `json:"phone"`
	Name          string     `json:"name"`
	LastVisit     *time.Time `json:"last_visit"`
	DaysSinceVisit *int      `json:"days_since_visit"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Template struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Broadcast struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	TemplateID int64     `json:"template_id"`
	Schedule   string    `json:"schedule"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type BroadcastLog struct {
	ID          int64     `json:"id"`
	BroadcastID int64     `json:"broadcast_id"`
	ContactID   int64     `json:"contact_id"`
	Phone       string    `json:"phone"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Error       string    `json:"error"`
	SentAt      time.Time `json:"sent_at"`
}

func New(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	d := &DB{sqlDB}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.Exec(`
	CREATE TABLE IF NOT EXISTS incoming_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		phone TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL,
		auto_action TEXT NOT NULL DEFAULT '',
		read INTEGER NOT NULL DEFAULT 0,
		received_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS contacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		phone TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		last_visit DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		return err
	}
	// Add last_visit column if upgrading from old schema
	d.Exec(`ALTER TABLE contacts ADD COLUMN last_visit DATETIME`)
	_, err = d.Exec(`
	CREATE TABLE IF NOT EXISTS templates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		body TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS broadcasts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		template_id INTEGER NOT NULL REFERENCES templates(id),
		schedule TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS broadcast_contacts (
		broadcast_id INTEGER NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
		contact_id INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
		PRIMARY KEY (broadcast_id, contact_id)
	);
	CREATE TABLE IF NOT EXISTS broadcast_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		broadcast_id INTEGER NOT NULL REFERENCES broadcasts(id),
		contact_id INTEGER NOT NULL REFERENCES contacts(id),
		phone TEXT NOT NULL,
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT '',
		sent_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`)
	return err
}

// Contacts

func (d *DB) CreateContact(phone, name string) (*Contact, error) {
	res, err := d.Exec(`INSERT INTO contacts (phone, name) VALUES (?, ?)`, phone, name)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Contact{ID: id, Phone: phone, Name: name, CreatedAt: time.Now()}, nil
}

func (d *DB) ListContacts() ([]Contact, error) {
	rows, err := d.Query(`
		SELECT id, phone, name, last_visit, created_at,
		CASE WHEN last_visit IS NOT NULL THEN CAST((julianday('now') - julianday(last_visit)) AS INTEGER) END
		FROM contacts ORDER BY last_visit ASC NULLS LAST, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.LastVisit, &c.CreatedAt, &c.DaysSinceVisit); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

func (d *DB) MarkVisit(id int64) error {
	_, err := d.Exec(`UPDATE contacts SET last_visit = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (d *DB) ListContactsNeedingVisit(days int) ([]Contact, error) {
	rows, err := d.Query(`
		SELECT id, phone, name, last_visit, created_at,
		CAST((julianday('now') - julianday(last_visit)) AS INTEGER)
		FROM contacts
		WHERE last_visit IS NOT NULL
		AND CAST((julianday('now') - julianday(last_visit)) AS INTEGER) >= ?
		ORDER BY last_visit ASC`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.LastVisit, &c.CreatedAt, &c.DaysSinceVisit); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

func (d *DB) DeleteContact(id int64) error {
	_, err := d.Exec(`DELETE FROM contacts WHERE id = ?`, id)
	return err
}

func (d *DB) ImportContacts(contacts []Contact) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, c := range contacts {
		_, err := tx.Exec(`INSERT OR IGNORE INTO contacts (phone, name) VALUES (?, ?)`, c.Phone, c.Name)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		count++
	}
	return count, tx.Commit()
}

// Templates

func (d *DB) CreateTemplate(name, body string) (*Template, error) {
	res, err := d.Exec(`INSERT INTO templates (name, body) VALUES (?, ?)`, name, body)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Template{ID: id, Name: name, Body: body, CreatedAt: time.Now()}, nil
}

func (d *DB) ListTemplates() ([]Template, error) {
	rows, err := d.Query(`SELECT id, name, body, created_at FROM templates ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var templates []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Body, &t.CreatedAt); err != nil {
			return nil, err
		}
		templates = append(templates, t)
	}
	return templates, nil
}

func (d *DB) GetTemplate(id int64) (*Template, error) {
	var t Template
	err := d.QueryRow(`SELECT id, name, body, created_at FROM templates WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Body, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) UpdateTemplate(id int64, name, body string) error {
	_, err := d.Exec(`UPDATE templates SET name = ?, body = ? WHERE id = ?`, name, body, id)
	return err
}

func (d *DB) DeleteTemplate(id int64) error {
	_, err := d.Exec(`DELETE FROM templates WHERE id = ?`, id)
	return err
}

// Broadcasts

func (d *DB) CreateBroadcast(name string, templateID int64, schedule string, contactIDs []int64) (*Broadcast, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	res, err := tx.Exec(`INSERT INTO broadcasts (name, template_id, schedule) VALUES (?, ?, ?)`, name, templateID, schedule)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	broadcastID, _ := res.LastInsertId()
	for _, cid := range contactIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO broadcast_contacts (broadcast_id, contact_id) VALUES (?, ?)`, broadcastID, cid); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Broadcast{ID: broadcastID, Name: name, TemplateID: templateID, Schedule: schedule, Status: "pending", CreatedAt: time.Now()}, nil
}

func (d *DB) ListBroadcasts() ([]Broadcast, error) {
	rows, err := d.Query(`SELECT id, name, template_id, schedule, status, created_at FROM broadcasts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var broadcasts []Broadcast
	for rows.Next() {
		var b Broadcast
		if err := rows.Scan(&b.ID, &b.Name, &b.TemplateID, &b.Schedule, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		broadcasts = append(broadcasts, b)
	}
	return broadcasts, nil
}

func (d *DB) GetBroadcastContacts(broadcastID int64) ([]Contact, error) {
	rows, err := d.Query(`
		SELECT c.id, c.phone, c.name, c.last_visit, c.created_at,
		CASE WHEN c.last_visit IS NOT NULL THEN CAST((julianday('now') - julianday(c.last_visit)) AS INTEGER) END
		FROM contacts c
		JOIN broadcast_contacts bc ON bc.contact_id = c.id
		WHERE bc.broadcast_id = ?`, broadcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.LastVisit, &c.CreatedAt, &c.DaysSinceVisit); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

func (d *DB) UpdateBroadcastStatus(id int64, status string) error {
	_, err := d.Exec(`UPDATE broadcasts SET status = ? WHERE id = ?`, status, id)
	return err
}

func (d *DB) DeleteBroadcast(id int64) error {
	_, err := d.Exec(`DELETE FROM broadcasts WHERE id = ?`, id)
	return err
}

func (d *DB) GetBroadcast(id int64) (*Broadcast, error) {
	var b Broadcast
	err := d.QueryRow(`SELECT id, name, template_id, schedule, status, created_at FROM broadcasts WHERE id = ?`, id).
		Scan(&b.ID, &b.Name, &b.TemplateID, &b.Schedule, &b.Status, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (d *DB) GetScheduledBroadcasts() ([]Broadcast, error) {
	rows, err := d.Query(`SELECT id, name, template_id, schedule, status, created_at FROM broadcasts WHERE schedule != '' AND status = 'pending'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var broadcasts []Broadcast
	for rows.Next() {
		var b Broadcast
		if err := rows.Scan(&b.ID, &b.Name, &b.TemplateID, &b.Schedule, &b.Status, &b.CreatedAt); err != nil {
			return nil, err
		}
		broadcasts = append(broadcasts, b)
	}
	return broadcasts, nil
}

// Logs

func (d *DB) AddLog(broadcastID, contactID int64, phone, name, status, errMsg string) error {
	_, err := d.Exec(`INSERT INTO broadcast_logs (broadcast_id, contact_id, phone, name, status, error) VALUES (?, ?, ?, ?, ?, ?)`,
		broadcastID, contactID, phone, name, status, errMsg)
	return err
}

func (d *DB) GetBroadcastLogs(broadcastID int64) ([]BroadcastLog, error) {
	rows, err := d.Query(`SELECT id, broadcast_id, contact_id, phone, name, status, error, sent_at FROM broadcast_logs WHERE broadcast_id = ? ORDER BY id DESC`, broadcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []BroadcastLog
	for rows.Next() {
		var l BroadcastLog
		if err := rows.Scan(&l.ID, &l.BroadcastID, &l.ContactID, &l.Phone, &l.Name, &l.Status, &l.Error, &l.SentAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

func (d *DB) GetBroadcastStats(broadcastID int64) (sent, failed int, err error) {
	err = d.QueryRow(`SELECT
		COUNT(CASE WHEN status='sent' THEN 1 END),
		COUNT(CASE WHEN status='failed' THEN 1 END)
		FROM broadcast_logs WHERE broadcast_id = ?`, broadcastID).Scan(&sent, &failed)
	return
}

type IncomingMessage struct {
	ID         int64     `json:"id"`
	Phone      string    `json:"phone"`
	Name       string    `json:"name"`
	Message    string    `json:"message"`
	AutoAction string    `json:"auto_action"`
	Read       bool      `json:"read"`
	ReceivedAt time.Time `json:"received_at"`
}

// Incoming messages

func (d *DB) SaveIncoming(phone, name, message, autoAction string) error {
	_, err := d.Exec(`INSERT INTO incoming_messages (phone, name, message, auto_action) VALUES (?, ?, ?, ?)`,
		phone, name, message, autoAction)
	return err
}

func (d *DB) ListIncoming(limit int) ([]IncomingMessage, error) {
	rows, err := d.Query(`
		SELECT id, phone, name, message, auto_action, read, received_at
		FROM incoming_messages ORDER BY received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []IncomingMessage
	for rows.Next() {
		var m IncomingMessage
		var readInt int
		if err := rows.Scan(&m.ID, &m.Phone, &m.Name, &m.Message, &m.AutoAction, &readInt, &m.ReceivedAt); err != nil {
			return nil, err
		}
		m.Read = readInt == 1
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (d *DB) MarkAllRead() error {
	_, err := d.Exec(`UPDATE incoming_messages SET read = 1 WHERE read = 0`)
	return err
}

func (d *DB) UnreadCount() int {
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM incoming_messages WHERE read = 0`).Scan(&n)
	return n
}

func (d *DB) ContactNameByPhone(phone string) string {
	var name string
	d.QueryRow(`SELECT name FROM contacts WHERE phone = ?`, phone).Scan(&name)
	return name
}

type Stats struct {
	TotalClients      int `json:"total_clients"`
	VisitsThisMonth   int `json:"visits_this_month"`
	VisitsThisWeek    int `json:"visits_this_week"`
	NeedReminder      int `json:"need_reminder"`     // not visited 21+ days
	NeverVisited      int `json:"never_visited"`
	MessagesSentTotal int `json:"messages_sent_total"`
	MessagesSentMonth int `json:"messages_sent_month"`
}

func (d *DB) GetStats() (*Stats, error) {
	s := &Stats{}
	err := d.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&s.TotalClients)
	if err != nil {
		return nil, err
	}
	d.QueryRow(`SELECT COUNT(*) FROM contacts WHERE last_visit >= date('now','start of month')`).Scan(&s.VisitsThisMonth)
	d.QueryRow(`SELECT COUNT(*) FROM contacts WHERE last_visit >= date('now','-7 days')`).Scan(&s.VisitsThisWeek)
	d.QueryRow(`SELECT COUNT(*) FROM contacts WHERE last_visit IS NOT NULL AND CAST((julianday('now')-julianday(last_visit)) AS INTEGER) >= 21`).Scan(&s.NeedReminder)
	d.QueryRow(`SELECT COUNT(*) FROM contacts WHERE last_visit IS NULL`).Scan(&s.NeverVisited)
	d.QueryRow(`SELECT COUNT(*) FROM broadcast_logs WHERE status='sent'`).Scan(&s.MessagesSentTotal)
	d.QueryRow(`SELECT COUNT(*) FROM broadcast_logs WHERE status='sent' AND sent_at >= date('now','start of month')`).Scan(&s.MessagesSentMonth)
	return s, nil
}

type WeeklyVisit struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

func (d *DB) GetWeeklyVisits() ([]WeeklyVisit, error) {
	rows, err := d.Query(`
		SELECT strftime('%w', last_visit) as dow, COUNT(*) as cnt
		FROM contacts
		WHERE last_visit >= date('now', '-30 days')
		GROUP BY dow ORDER BY dow`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}
	result := make([]WeeklyVisit, 7)
	for i, d := range days {
		result[i] = WeeklyVisit{Day: d, Count: 0}
		_ = i
	}
	for rows.Next() {
		var dow string
		var cnt int
		rows.Scan(&dow, &cnt)
		if i := int(dow[0] - '0'); i >= 0 && i < 7 {
			result[i].Count = cnt
		}
	}
	return result, nil
}

type TopClient struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Visits    int    `json:"visits"`
	LastVisit string `json:"last_visit"`
}

func (d *DB) GetTopClients(limit int) ([]TopClient, error) {
	rows, err := d.Query(`
		SELECT c.name, c.phone,
		COUNT(bl.id) as visits,
		MAX(bl.sent_at) as last_contact
		FROM contacts c
		LEFT JOIN broadcast_logs bl ON bl.phone = c.phone AND bl.status = 'sent'
		GROUP BY c.id ORDER BY c.last_visit DESC NULLS LAST LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var clients []TopClient
	for rows.Next() {
		var c TopClient
		rows.Scan(&c.Name, &c.Phone, &c.Visits, &c.LastVisit)
		clients = append(clients, c)
	}
	return clients, nil
}

func (d *DB) GetTodayVisits() ([]Contact, error) {
	rows, err := d.Query(`
		SELECT id, phone, name, last_visit, created_at, 0
		FROM contacts
		WHERE date(last_visit) = date('now')
		ORDER BY last_visit DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []Contact
	for rows.Next() {
		var c Contact
		var days int
		if err := rows.Scan(&c.ID, &c.Phone, &c.Name, &c.LastVisit, &c.CreatedAt, &days); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

func (d *DB) FindContactByName(query string) (*Contact, error) {
	var c Contact
	var days *int
	err := d.QueryRow(`
		SELECT id, phone, name, last_visit, created_at,
		CASE WHEN last_visit IS NOT NULL THEN CAST((julianday('now') - julianday(last_visit)) AS INTEGER) END
		FROM contacts WHERE lower(name) LIKE lower('%'||?||'%') OR phone LIKE '%'||? LIMIT 1`,
		query, query).Scan(&c.ID, &c.Phone, &c.Name, &c.LastVisit, &c.CreatedAt, &days)
	if err != nil {
		return nil, err
	}
	c.DaysSinceVisit = days
	return &c, nil
}

func init() {
	_ = fmt.Sprintf
}
