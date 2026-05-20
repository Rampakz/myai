package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"flag"
)

const historyFile = "history.json"

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools"`
	Stream   bool      `json:"stream"`
}

type Response struct {
	Message    Message `json:"message"`
	DoneReason string  `json:"done_reason"`
}

var history []Message

var tools = []Tool{
	{
		Type: "function",
		Function: struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}{
			Name:        "get_time",
			Description: "Возвращает текущую дату и время",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	},
	{
		Type: "function",
		Function: struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}{
			Name:        "read_file",
			Description: "Читает содержимое файла. Аргумент path — строка с путём к файлу.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		Type: "function",
		Function: struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}{
			Name:        "write_file",
			Description: "Записывает текст в файл. path — путь к файлу, content — содержимое.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		Type: "function",
		Function: struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  any    `json:"parameters"`
		}{
			Name:        "run_code",
			Description: "Запускает Go файл и возвращает вывод. Если ошибка — возвращает текст ошибки компиляции.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	},
}

// llama3.2 иногда возвращает аргумент как {"type":"string","value":"main.go"} вместо просто строки
func extractStringArg(args json.RawMessage, key string) string {
	var raw map[string]json.RawMessage
	if json.Unmarshal(args, &raw) != nil {
		return ""
	}
	val, ok := raw[key]
	if !ok {
		return ""
	}
	// попытка 1: простая строка
	var s string
	if json.Unmarshal(val, &s) == nil {
		return s
	}
	// попытка 2: объект с полем "value"
	var obj map[string]string
	if json.Unmarshal(val, &obj) == nil {
		return obj["value"]
	}
	return ""
}

// выполняем инструмент и возвращаем результат
func executeTool(name string, args json.RawMessage) string {
	switch name {
	case "get_time":
		return time.Now().Format("2006-01-02 15:04:05")
	case "read_file":
		path := extractStringArg(args, "path")
		if path == "" {
			return "Ошибка: не указан путь к файлу"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "Ошибка: " + err.Error()
		}
		return string(data)
	case "write_file":
		path := extractStringArg(args, "path")
		content := extractStringArg(args, "content")
		if path == "" {
			return "Ошибка: не указан путь к файлу"
		}
		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			return "Ошибка: " + err.Error()
		}
		return "Файл " + path + " успешно записан"
	case "run_code":
		path := extractStringArg(args, "path")
		if path == "" {
			return "Ошибка: не указан путь к файлу"
		}
		out, err := exec.Command("go", "run", path).CombinedOutput()
		if err != nil {
			return "Ошибка компиляции:\n" + string(out)
		}
		return "Результат:\n" + string(out)
	}
	return "неизвестный инструмент"
}

func callAPI(messages []Message) Response {
	groqKey := os.Getenv("GROQ_API_KEY")

	if groqKey != "" {
		return callGroq(messages, groqKey)
	}
	return callOllama(messages)
}

func callOllama(messages []Message) Response {
	reqBody, _ := json.Marshal(Request{
		Model:    "qwen2.5-coder:3b",
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	})
	resp, err := http.Post("http://localhost:11434/api/chat", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var result Response
	json.Unmarshal(data, &result)
	return result
}

// OpenAI-совместимый формат для Groq
type GroqRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type GroqResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func callGroq(messages []Message, apiKey string) Response {
	reqBody, _ := json.Marshal(GroqRequest{
		Model:    "llama-3.1-8b-instant",
		Messages: messages,
		Stream:   false,
	})

	req, _ := http.NewRequest("POST", "https://api.groq.com/openai/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var groqResp GroqResponse
	json.Unmarshal(data, &groqResp)

	if len(groqResp.Choices) == 0 {
		return Response{}
	}
	return Response{Message: groqResp.Choices[0].Message}
}

type toolCallText struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Arguments  json.RawMessage `json:"arguments"`
}

func (t *toolCallText) args() json.RawMessage {
	if len(t.Arguments) > 0 {
		return t.Arguments
	}
	return t.Parameters
}

// некоторые модели возвращают вызов инструмента как JSON в content, иногда в ```json блоке
func parseToolCallFromContent(content string) *toolCallText {
	content = strings.TrimSpace(content)

	// убираем ```json ... ``` обёртку
	if strings.HasPrefix(content, "```") {
		start := strings.Index(content, "\n")
		end := strings.LastIndex(content, "```")
		if start != -1 && end > start {
			content = strings.TrimSpace(content[start+1 : end])
		}
	}

	if !strings.Contains(content, `"name"`) {
		return nil
	}

	// ищем первый валидный JSON объект с полем name
	dec := json.NewDecoder(strings.NewReader(content))
	for dec.More() {
		var tc toolCallText
		if err := dec.Decode(&tc); err != nil {
			break
		}
		if tc.Name != "" {
			return &tc
		}
	}
	return nil
}

func ask(userInput string) {
	history = append(history, Message{Role: "user", Content: userInput})

	const maxToolCalls = 10
	toolCallCount := 0

	for {
		result := callAPI(history)
		msg := result.Message

		// вариант 1: нативные tool_calls
		if len(msg.ToolCalls) > 0 {
			history = append(history, msg)
			for _, tc := range msg.ToolCalls {
				toolResult := executeTool(tc.Function.Name, tc.Function.Arguments)
				fmt.Printf("[инструмент: %s → %s]\n", tc.Function.Name, toolResult)
				history = append(history, Message{Role: "tool", Content: toolResult})
			}
			toolCallCount++
			if toolCallCount >= maxToolCalls {
				fmt.Println("[лимит инструментов достигнут]")
				break
			}
			continue
		}

		// вариант 2: модель вернула JSON в content
		if tc := parseToolCallFromContent(msg.Content); tc != nil {
			history = append(history, msg)
			toolResult := executeTool(tc.Name, tc.args())
			fmt.Printf("[инструмент: %s → %s]\n", tc.Name, toolResult)
			history = append(history, Message{Role: "tool", Content: toolResult})
			toolCallCount++
			if toolCallCount >= maxToolCalls {
				fmt.Println("[лимит инструментов достигнут]")
				break
			}
			continue
		}

		// обычный ответ
		fmt.Println("Бот:", msg.Content)
		history = append(history, Message{Role: "assistant", Content: msg.Content})
		saveHistory()
		break
	}
}

func loadHistory() {
	data, err := os.ReadFile(historyFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &history)
	fmt.Printf("Загружено %d сообщений из истории.\n", len(history))
}

func saveHistory() {
	data, _ := json.MarshalIndent(history, "", "  ")
	os.WriteFile(historyFile, data, 0644)
}

func main() {
	tg := flag.Bool("tg", false, "запустить Telegram бота")
	flag.Parse()

	if *tg {
		startTelegramBot()
		return
	}

	loadHistory()

	if len(history) == 0 {
		history = append(history, Message{
			Role:    "system",
			Content: "Ты полезный русскоязычный ассистент. Отвечай коротко и по делу. Используй инструменты когда нужно.",
		})
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Чат-бот запущен. Команды: 'выход', 'очистить'")
	fmt.Println("---")

	for {
		fmt.Print("Ты: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())

		if input == "" {
			continue
		}

		switch input {
		case "выход":
			fmt.Println("Пока!")
			return
		case "очистить":
			history = nil
			os.Remove(historyFile)
			fmt.Println("История очищена.")
			history = append(history, Message{
				Role:    "system",
				Content: "Ты полезный русскоязычный ассистент. Отвечай коротко и по делу. Используй инструменты когда нужно.",
			})
			continue
		default:
			// команда "агент <задача>"
			if strings.HasPrefix(input, "агент ") {
				task := strings.TrimPrefix(input, "агент ")
				runMultiAgent(task)
				continue
			}
			// команда "индекс <файл>"
			if strings.HasPrefix(input, "индекс ") {
				path := strings.TrimPrefix(input, "индекс ")
				if err := indexFile(path); err != nil {
					fmt.Println("Ошибка:", err)
				} else {
					fmt.Println("Готово! Теперь можешь задавать вопросы по файлу.")
				}
				continue
			}
		}

		// если есть проиндексированные данные — добавляем контекст отдельным сообщением
		if len(vectorStore) > 0 {
			ctx := buildContext(input)
			if ctx != "" {
				history = append(history, Message{
					Role:    "user",
					Content: ctx,
				})
				history = append(history, Message{
					Role:    "assistant",
					Content: "Понял, учту эту информацию при ответе.",
				})
			}
		}

		ask(input)
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
