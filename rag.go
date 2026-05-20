package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
)

type Chunk struct {
	Text   string
	Source string
	Vector []float64
}

var vectorStore []Chunk

// получаем эмбеддинг (вектор) для текста
func embed(text string) ([]float64, error) {
	body, _ := json.Marshal(map[string]string{
		"model":  "nomic-embed-text",
		"prompt": text,
	})

	resp, err := http.Post("http://localhost:11434/api/embeddings", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	json.Unmarshal(data, &result)
	return result.Embedding, nil
}

// косинусное сходство — насколько два вектора похожи (0..1)
func cosineSimilarity(a, b []float64) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// разбиваем файл на куски по ~500 символов
func chunkText(text, source string) []Chunk {
	const chunkSize = 500
	var chunks []Chunk
	runes := []rune(text)

	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, Chunk{
			Text:   string(runes[i:end]),
			Source: source,
		})
	}
	return chunks
}

// индексируем файл — читаем, разбиваем, эмбеддируем
func indexFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	chunks := chunkText(string(data), path)
	fmt.Printf("Индексирую %s (%d чанков)...\n", path, len(chunks))

	for i, chunk := range chunks {
		vec, err := embed(chunk.Text)
		if err != nil {
			return err
		}
		chunks[i].Vector = vec
		vectorStore = append(vectorStore, chunks[i])
	}
	return nil
}

// ищем топ-3 самых релевантных чанка
func search(query string, topK int) []Chunk {
	if len(vectorStore) == 0 {
		return nil
	}

	queryVec, err := embed(query)
	if err != nil {
		return nil
	}

	type scored struct {
		chunk Chunk
		score float64
	}

	var results []scored
	for _, chunk := range vectorStore {
		score := cosineSimilarity(queryVec, chunk.Vector)
		results = append(results, scored{chunk, score})
	}

	// сортируем по убыванию score
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	var top []Chunk
	for i := 0; i < topK && i < len(results); i++ {
		top = append(top, results[i].chunk)
	}
	return top
}

// строим контекст для промпта из найденных чанков
func buildContext(query string) string {
	chunks := search(query, 3)
	if len(chunks) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Используй эту информацию для ответа:\n\n")
	for _, c := range chunks {
		sb.WriteString(fmt.Sprintf("[из %s]:\n%s\n\n", c.Source, c.Text))
	}
	return sb.String()
}
