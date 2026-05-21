package main

import (
	"database/sql"
	"encoding/json"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./bot.db")
	if err != nil {
		log.Fatal(err)
	}

	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		chat_id INTEGER PRIMARY KEY,
		history TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)

	db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER,
		username TEXT,
		items TEXT,
		total INTEGER,
		status TEXT DEFAULT 'new',
		created_at TEXT
	)`)
}

func loadUserHistory(chatID int64) []Message {
	var histJSON string
	err := db.QueryRow("SELECT history FROM users WHERE chat_id = ?", chatID).Scan(&histJSON)
	if err != nil {
		// новый пользователь
		return []Message{{
			Role:    "system",
			Content: "Ты полезный русскоязычный ассистент. Отвечай коротко и по делу. Используй инструменты ТОЛЬКО если пользователь явно просит: узнать время, прочитать файл, написать или запустить код. На приветствия и обычные вопросы отвечай текстом без инструментов.",
		}}
	}

	var msgs []Message
	json.Unmarshal([]byte(histJSON), &msgs)
	return msgs
}

func saveUserHistory(chatID int64, msgs []Message) {
	data, _ := json.Marshal(msgs)

	db.Exec(`INSERT INTO users (chat_id, history) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET history = ?`,
		chatID, string(data), string(data))
}

func clearUserHistory(chatID int64) {
	db.Exec("DELETE FROM users WHERE chat_id = ?", chatID)
}

type Order struct {
	ID        int    `json:"id"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Items     string `json:"items"`
	Total     int    `json:"total"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func getOrders() []Order {
	rows, err := db.Query(`SELECT id, user_id, username, items, total, status, created_at FROM orders ORDER BY id DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		rows.Scan(&o.ID, &o.UserID, &o.Username, &o.Items, &o.Total, &o.Status, &o.CreatedAt)
		orders = append(orders, o)
	}
	return orders
}

func updateOrderStatus(id int, status string) (int64, error) {
	var userID int64
	db.QueryRow(`SELECT user_id FROM orders WHERE id = ?`, id).Scan(&userID)
	_, err := db.Exec(`UPDATE orders SET status = ? WHERE id = ?`, status, id)
	return userID, err
}
