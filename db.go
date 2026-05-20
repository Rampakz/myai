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
