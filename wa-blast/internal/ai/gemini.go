package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Client struct {
	client *genai.Client
	model  string
}

func New(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY not set")
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &Client{client: client, model: "gemini-2.0-flash"}, nil
}

func (c *Client) Close() {
	c.client.Close()
}

// ReplyToClient generates a smart reply for an incoming WhatsApp message from a barber's client.
func (c *Client) ReplyToClient(ctx context.Context, clientName, clientPhone, message string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	model := c.client.GenerativeModel(c.model)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(`Ты — дружелюбный ИИ-ассистент барбершопа. Отвечай коротко, по-русски, тепло и профессионально.
Клиент пишет в WhatsApp барберу. Твоя задача — помочь: ответить на вопросы, помочь записаться, рассказать об услугах.
Услуги: стрижка, борода, укладка. Барбершоп работает ежедневно 10:00-21:00.
Если клиент хочет записаться, скажи что барбер свяжется для подтверждения времени.
Не выдумывай конкретные цены или свободное время — барбер уточнит.
Отвечай 1-2 предложения максимум.`),
		},
	}

	prompt := message
	if clientName != "" {
		prompt = fmt.Sprintf("[Клиент: %s] %s", clientName, message)
	}

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return extractText(resp), nil
}

// GenerateBroadcast creates a broadcast message based on a description.
func (c *Client) GenerateBroadcast(ctx context.Context, description, clientName string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	model := c.client.GenerativeModel(c.model)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(`Ты — копирайтер барбершопа. Пишешь короткие, дружелюбные WhatsApp-сообщения для рассылки клиентам.
Используй {{name}} для имени клиента. Сообщение должно быть тёплым, неформальным, до 3 предложений.
Добавь 1-2 уместных эмодзи. Пиши на русском языке.`),
		},
	}

	prompt := fmt.Sprintf("Напиши WhatsApp-сообщение для рассылки барбершопа. Тема: %s", description)
	if clientName != "" {
		prompt += fmt.Sprintf(". Пример имени клиента: %s", clientName)
	}

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return extractText(resp), nil
}

type ClientInsight struct {
	Phone      string
	Name       string
	DaysSince  int
	Suggestion string
}

// AnalyzeClients returns AI-generated insights for clients who haven't visited recently.
func (c *Client) AnalyzeClients(ctx context.Context, clients []ClientInfo) ([]ClientInsight, error) {
	if len(clients) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	model := c.client.GenerativeModel(c.model)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(`Ты — аналитик барбершопа. Для каждого клиента напиши краткую рекомендацию (1 предложение) что написать ему в WhatsApp, чтобы он вернулся. Учитывай сколько дней прошло с последнего визита. Пиши по-русски.`),
		},
	}

	var sb strings.Builder
	sb.WriteString("Клиенты барбершопа, которые давно не приходили:\n")
	for i, cl := range clients {
		if i >= 10 {
			break
		}
		name := cl.Name
		if name == "" {
			name = cl.Phone
		}
		sb.WriteString(fmt.Sprintf("- %s: %d дней без визита\n", name, cl.DaysSince))
	}
	sb.WriteString("\nДля каждого клиента напиши: имя: рекомендация")

	resp, err := model.GenerateContent(ctx, genai.Text(sb.String()))
	if err != nil {
		return nil, err
	}

	text := extractText(resp)
	lines := strings.Split(text, "\n")

	var insights []ClientInsight
	for i, cl := range clients {
		if i >= 10 {
			break
		}
		name := cl.Name
		if name == "" {
			name = cl.Phone
		}
		suggestion := ""
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), strings.ToLower(name)) {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					suggestion = strings.TrimSpace(parts[1])
				}
				break
			}
		}
		if suggestion == "" && i < len(lines) {
			suggestion = strings.TrimSpace(lines[i])
		}
		insights = append(insights, ClientInsight{
			Phone:      cl.Phone,
			Name:       cl.Name,
			DaysSince:  cl.DaysSince,
			Suggestion: suggestion,
		})
	}
	return insights, nil
}

type ClientInfo struct {
	Phone     string
	Name      string
	DaysSince int
}

func extractText(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}
	cand := resp.Candidates[0]
	if cand.Content == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range cand.Content.Parts {
		if t, ok := part.(genai.Text); ok {
			sb.WriteString(string(t))
		}
	}
	return strings.TrimSpace(sb.String())
}
