package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func startTelegramBot() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		token = "7799784086:AAEMusJcpHra2n4vwrEBQSijyuejXVP01sw"
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal(err)
	}

	initDB()
	fmt.Printf("Бот запущен: @%s\n", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		text := strings.TrimSpace(update.Message.Text)

		var reply string

		switch {
		case text == "/start":
			reply = "Привет! Я AI-ассистент. Спрашивай что угодно.\n\nКоманды:\n/clear — очистить историю\n/time — текущее время"

		case text == "/clear":
			clearUserHistory(chatID)
			reply = "История очищена."

		case text == "/time":
			reply = executeTool("get_time", nil)

		default:
			fmt.Printf("[%d] получено: %s\n", chatID, text)
			history = loadUserHistory(chatID)
			reply = askTelegram(text)
			saveUserHistory(chatID, history)
			fmt.Printf("[%d] ответ: %s\n", chatID, truncate(reply, 80))
		}

		if reply == "" {
			reply = "Думаю... попробуй ещё раз."
		}

		msg := tgbotapi.NewMessage(chatID, reply)
		msg.ParseMode = "Markdown"
		if _, err := bot.Send(msg); err != nil {
			msg.ParseMode = ""
			bot.Send(msg)
		}
	}
}

// ключевые слова при которых включаем инструменты
func needsTools(input string) bool {
	keywords := []string{"файл", "код", "время", "запусти", "напиши", "создай", "прочитай", "запусти"}
	lower := strings.ToLower(input)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// версия ask() которая возвращает строку вместо печати в консоль
func askTelegram(userInput string) string {
	history = append(history, Message{Role: "user", Content: userInput})

	// для простого чата — без инструментов
	if !needsTools(userInput) {
		reqBody, _ := json.Marshal(map[string]any{
			"model":    "qwen2.5-coder:3b",
			"messages": history,
			"stream":   false,
		})
		resp, _ := http.Post("http://localhost:11434/api/chat", "application/json", strings.NewReader(string(reqBody)))
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var result Response
		json.Unmarshal(data, &result)
		reply := result.Message.Content
		history = append(history, Message{Role: "assistant", Content: reply})
		return reply
	}

	// если нужны инструменты — полный цикл
	const maxToolCalls = 5
	toolCallCount := 0
	var finalResponse strings.Builder

	for {
		result := callAPI(history)
		msg := result.Message

		if len(msg.ToolCalls) > 0 {
			history = append(history, msg)
			for _, tc := range msg.ToolCalls {
				toolResult := executeTool(tc.Function.Name, tc.Function.Arguments)
				finalResponse.WriteString(fmt.Sprintf("_%s_: %s\n", tc.Function.Name, truncate(toolResult, 100)))
				history = append(history, Message{Role: "tool", Content: toolResult})
			}
			toolCallCount++
			if toolCallCount >= maxToolCalls {
				break
			}
			continue
		}

		if tc := parseToolCallFromContent(msg.Content); tc != nil {
			history = append(history, msg)
			toolResult := executeTool(tc.Name, tc.args())
			finalResponse.WriteString(fmt.Sprintf("_%s_: %s\n", tc.Name, truncate(toolResult, 100)))
			history = append(history, Message{Role: "tool", Content: toolResult})
			toolCallCount++
			if toolCallCount >= maxToolCalls {
				break
			}
			continue
		}

		history = append(history, Message{Role: "assistant", Content: msg.Content})
		finalResponse.WriteString(msg.Content)
		break
	}

	return finalResponse.String()
}
