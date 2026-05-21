package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type OrderItem struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Qty   int    `json:"qty"`
	Price int    `json:"price"`
}

type OrderRequest struct {
	UserID   int64       `json:"user_id"`
	Username string      `json:"username"`
	Items    []OrderItem `json:"items"`
	Total    int         `json:"total"`
}

func startServer(bot interface{ Send(interface{}) (interface{}, error) }, adminID int64) {
	// статичные файлы (HTML, CSS, JS)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	http.HandleFunc("/api/order", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}

		var req OrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}

		// сохраняем заказ в БД
		orderID := saveOrder(req)

		// уведомляем админа
		msg := fmt.Sprintf("🛎 *Новый заказ #%04d*\n\n", orderID)
		msg += fmt.Sprintf("👤 @%s\n", req.Username)
		msg += "─────────────\n"
		for _, item := range req.Items {
			msg += fmt.Sprintf("• %s × %d — %d ₽\n", item.Name, item.Qty, item.Price*item.Qty)
		}
		msg += fmt.Sprintf("─────────────\n💰 *Итого: %d ₽*", req.Total)

		notifyAdmin(adminID, msg)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]int{"order_id": orderID})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Println("HTTP сервер запущен на порту", port)
	http.ListenAndServe(":"+port, nil)
}

func saveOrder(req OrderRequest) int {
	itemsJSON, _ := json.Marshal(req.Items)
	result, err := db.Exec(
		`INSERT INTO orders (user_id, username, items, total, status, created_at) VALUES (?, ?, ?, ?, 'new', ?)`,
		req.UserID, req.Username, string(itemsJSON), req.Total, time.Now().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return 0
	}
	id, _ := result.LastInsertId()
	return int(id)
}

var adminNotify func(int64, string)

func notifyAdmin(chatID int64, text string) {
	if adminNotify != nil {
		adminNotify(chatID, text)
	}
}
