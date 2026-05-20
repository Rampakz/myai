package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Agent struct {
	Name    string
	System  string
	history []Message
}

func newAgent(name, system string) *Agent {
	return &Agent{
		Name:   name,
		System: system,
		history: []Message{
			{Role: "system", Content: system},
		},
	}
}

func (a *Agent) chat(input string) string {
	a.history = append(a.history, Message{Role: "user", Content: input})

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "qwen2.5-coder:3b",
		"messages": a.history,
		"stream":   false,
	})

	resp, err := http.Post("http://localhost:11434/api/chat", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		return "ошибка: " + err.Error()
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result Response
	json.Unmarshal(data, &result)

	reply := result.Message.Content
	a.history = append(a.history, Message{Role: "assistant", Content: reply})
	return reply
}

// Планировщик — разбивает задачу на шаги
var planner = newAgent("Планировщик", `Ты планировщик задач.
Получаешь задачу от пользователя и возвращаешь план в виде нумерованного списка конкретных шагов.
Каждый шаг должен быть выполним через инструменты: write_file, run_code, read_file.
Отвечай ТОЛЬКО списком шагов, без лишних слов. Максимум 5 шагов.
Пример:
1. Написать файл calculator.go с функцией сложения
2. Запустить calculator.go и проверить вывод`)

// Исполнитель — выполняет каждый шаг через инструменты
var executor = newAgent("Исполнитель", `Ты исполнитель задач.
Получаешь один конкретный шаг и выполняешь его используя инструменты write_file, run_code, read_file.
Используй инструменты напрямую. Отвечай кратко что сделал.`)

func runMultiAgent(userTask string) {
	fmt.Println("\n╔══ МУЛЬТИ-АГЕНТ ЗАПУЩЕН ══╗")
	fmt.Printf("Задача: %s\n\n", userTask)

	// Шаг 1: планировщик строит план
	fmt.Println("🧠 [Планировщик] думает...")
	plan := planner.chat(userTask)
	fmt.Println("📋 План:")
	fmt.Println(plan)
	fmt.Println()

	// парсим шаги из плана
	steps := parsePlan(plan)
	if len(steps) == 0 {
		fmt.Println("Не удалось разобрать план")
		return
	}

	// Шаг 2: исполнитель выполняет каждый шаг
	for i, step := range steps {
		fmt.Printf("⚙️  [Исполнитель] Шаг %d: %s\n", i+1, step)

		// используем существующую логику ask() но без вывода в историю чата
		result := executeStep(step)
		fmt.Printf("   ✓ %s\n\n", result)
	}

	fmt.Println("╚══ ГОТОВО ══╝")
}

// разбираем нумерованный список на шаги
func parsePlan(plan string) []string {
	var steps []string
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		// ищем строки вида "1. ...", "2. ..."
		if line[0] >= '1' && line[0] <= '9' && len(line) > 2 && (line[1] == '.' || line[2] == '.') {
			step := strings.TrimSpace(line[2:])
			if len(step) > 0 {
				steps = append(steps, step)
			}
		}
	}
	return steps
}

// выполняем один шаг через исполнителя с инструментами
func executeStep(step string) string {
	agentHistory := []Message{
		{Role: "system", Content: executor.System},
		{Role: "user", Content: step},
	}

	const maxCalls = 5
	calls := 0

	for calls < maxCalls {
		reqBody, _ := json.Marshal(Request{
			Model:    "qwen2.5-coder:3b",
			Messages: agentHistory,
			Tools:    tools,
			Stream:   false,
		})

		resp, _ := http.Post("http://localhost:11434/api/chat", "application/json", strings.NewReader(string(reqBody)))
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result Response
		json.Unmarshal(data, &result)
		msg := result.Message

		if len(msg.ToolCalls) > 0 {
			agentHistory = append(agentHistory, msg)
			for _, tc := range msg.ToolCalls {
				toolResult := executeTool(tc.Function.Name, tc.Function.Arguments)
				fmt.Printf("      [%s] → %s\n", tc.Function.Name, truncate(toolResult, 80))
				agentHistory = append(agentHistory, Message{Role: "tool", Content: toolResult})
			}
			calls++
			continue
		}

		if tc := parseToolCallFromContent(msg.Content); tc != nil {
			agentHistory = append(agentHistory, msg)
			toolResult := executeTool(tc.Name, tc.args())
			fmt.Printf("      [%s] → %s\n", tc.Name, truncate(toolResult, 80))
			agentHistory = append(agentHistory, Message{Role: "tool", Content: toolResult})
			calls++
			continue
		}

		return msg.Content
	}
	return "выполнено"
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
