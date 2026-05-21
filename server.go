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

	// GET /api/orders?token=xxx
	http.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		if r.URL.Query().Get("token") != adminToken() {
			http.Error(w, "unauthorized", 401)
			return
		}
		orders := getOrders()
		if orders == nil {
			orders = []Order{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orders)
	})

	// POST /api/orders/status  body: {id, status, token}
	http.HandleFunc("/api/orders/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var body struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
			Token  string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Token != adminToken() {
			http.Error(w, "unauthorized", 401)
			return
		}
		userID, err := updateOrderStatus(body.ID, body.Status)
		if err != nil {
			http.Error(w, "db error", 500)
			return
		}
		// уведомить клиента если заказ готов
		if body.Status == "ready" && userID > 0 && notifyUserFn != nil {
			notifyUserFn(userID, fmt.Sprintf("☕ Ваш заказ *#%04d* готов! Заберите у стойки.", body.ID))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
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
var notifyUserFn func(int64, string)

func notifyAdmin(chatID int64, text string) {
	if adminNotify != nil {
		adminNotify(chatID, text)
	}
}

func adminToken() string {
	if t := os.Getenv("ADMIN_TOKEN"); t != "" {
		return t
	}
	return "admin123"
}
