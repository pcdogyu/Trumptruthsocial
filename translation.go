package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultTranslationPrompt = "Translate the following text into the target language, preserving meaning and tone:"

type translationAPIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type translationAPIRequest struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []translationAPIMessage `json:"messages"`
	Temperature float64                 `json:"temperature,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
}

func enrichPostTranslation(cfg Config, post *Post, allowRetry bool) {
	if post == nil {
		return
	}
	if !cfg.Translation.Enabled {
		return
	}
	if strings.TrimSpace(post.Content) == "" {
		return
	}
	if strings.TrimSpace(post.TranslatedContent) != "" && strings.TrimSpace(post.TranslationError) == "" {
		return
	}
	if !allowRetry && strings.TrimSpace(post.TranslationError) != "" {
		return
	}

	translated, err := translateText(cfg, post.Content)
	if err != nil {
		post.TranslationError = err.Error()
		if strings.TrimSpace(post.TranslatedContent) == "" {
			post.TranslatedContent = ""
		}
		return
	}
	post.TranslatedAt = time.Now().UTC().Format(time.RFC3339)
	post.TranslatedContent = strings.TrimSpace(translated)
	post.TranslationError = ""
}

func hydratePostTranslationFromStore(store *PostStore, post *Post) {
	if store == nil || post == nil {
		return
	}
	if stored, ok := store.GetPostByID(post.ID); ok {
		post.TranslatedContent = stored.TranslatedContent
		post.TranslatedAt = stored.TranslatedAt
		post.TranslationError = stored.TranslationError
	}
}

func translateText(cfg Config, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	if !cfg.Translation.Enabled {
		return "", errors.New("翻译功能未启用")
	}

	apiURL := strings.TrimSpace(cfg.Translation.APIURL)
	if apiURL == "" || strings.Contains(apiURL, "YOUR_TRANSLATION_API_URL") {
		return "", errors.New("翻译 API URL 未配置")
	}

	requestText := buildTranslationRequestText(cfg, content)
	if requestText == "" {
		return "", errors.New("翻译请求内容为空")
	}

	payload := translationAPIRequest{
		Messages: []translationAPIMessage{
			{
				Role:    "user",
				Content: requestText,
			},
		},
		Temperature: 0,
	}

	model := strings.TrimSpace(cfg.Translation.Model)
	if model != "" && !strings.Contains(model, "YOUR_TRANSLATION_MODEL") {
		payload.Model = model
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	timeout := cfg.Translation.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	apiKey := strings.TrimSpace(cfg.Translation.APIKey)
	if apiKey != "" && !strings.Contains(apiKey, "YOUR_TRANSLATION_API_KEY") {
		bearerToken := strings.TrimSpace(strings.TrimPrefix(apiKey, "Bearer "))
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
			req.Header.Set("X-API-Key", bearerToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("翻译 API 请求失败: %s", translationHTTPErrorMessage(resp.Status, respBody))
	}

	text, err := extractTranslationText(respBody)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("翻译 API 返回空内容")
	}
	return text, nil
}

func buildTranslationRequestText(cfg Config, content string) string {
	prompt := strings.TrimSpace(cfg.Translation.Prompt)
	if prompt == "" || strings.Contains(prompt, "YOUR_TRANSLATION") {
		prompt = defaultTranslationPrompt
	}

	replacements := map[string]string{
		"{source_language}": strings.TrimSpace(cfg.Translation.SourceLanguage),
		"{target_language}": strings.TrimSpace(cfg.Translation.TargetLanguage),
		"{content}":         content,
	}

	hasContentPlaceholder := strings.Contains(prompt, "{content}")
	for key, value := range replacements {
		prompt = strings.ReplaceAll(prompt, key, value)
	}
	if hasContentPlaceholder {
		return strings.TrimSpace(prompt)
	}

	sourceLanguage := strings.TrimSpace(cfg.Translation.SourceLanguage)
	targetLanguage := strings.TrimSpace(cfg.Translation.TargetLanguage)
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n")
	if sourceLanguage != "" {
		b.WriteString("Source language: ")
		b.WriteString(sourceLanguage)
		b.WriteString("\n")
	}
	if targetLanguage != "" {
		b.WriteString("Target language: ")
		b.WriteString(targetLanguage)
		b.WriteString("\n")
	}
	b.WriteString("Content:\n")
	b.WriteString(content)
	return strings.TrimSpace(b.String())
}

func translationHTTPErrorMessage(status string, body []byte) string {
	if len(body) == 0 {
		return "翻译 API 请求失败: " + status
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Sprintf("翻译 API 请求失败: %s, 返回: %s", status, strings.TrimSpace(string(body)))
	}

	for _, key := range []string{"error", "message", "detail", "description"} {
		if value := extractStringFromAny(payload[key]); value != "" {
			return fmt.Sprintf("翻译 API 请求失败: %s, 详情: %s", status, value)
		}
	}

	if text := extractTranslationTextFromAny(payload); text != "" {
		return fmt.Sprintf("翻译 API 请求失败: %s, 返回: %s", status, text)
	}

	return "翻译 API 请求失败: " + status
}

func extractTranslationText(body []byte) (string, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	text := extractTranslationTextFromAny(payload)
	if text == "" {
		return "", errors.New("翻译 API 未返回可识别的译文")
	}
	return text, nil
}

func extractTranslationTextFromAny(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if text := extractTranslationTextFromMap(v); text != "" {
			return text
		}
		for _, key := range []string{"data", "result", "response"} {
			if text := extractTranslationTextFromAny(v[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range v {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

func extractTranslationTextFromMap(payload map[string]any) string {
	if text := extractStringFromAny(payload["output_text"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["translated_text"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["translation"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["text"]); text != "" {
		return text
	}

	if choices, ok := payload["choices"].([]any); ok {
		for _, choice := range choices {
			if text := extractTranslationTextFromAny(choice); text != "" {
				return text
			}
		}
	}
	if data, ok := payload["data"].([]any); ok {
		for _, item := range data {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	}

	if nested, ok := payload["message"].(map[string]any); ok {
		if text := extractStringFromAny(nested["content"]); text != "" {
			return text
		}
	}
	if nested, ok := payload["content"].([]any); ok {
		for _, item := range nested {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractStringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"message", "detail", "description", "error", "msg"} {
			if text := extractStringFromAny(v[key]); text != "" {
				return text
			}
		}
		if text := extractStringFromAny(v["content"]); text != "" {
			return text
		}
		if text := extractStringFromAny(v["text"]); text != "" {
			return text
		}
	case []any:
		for _, item := range v {
			if text := extractStringFromAny(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func backfillStoredTranslations(store *PostStore) {
	if store == nil {
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		return
	}
	if !cfg.Translation.Enabled {
		return
	}

	posts := store.GetAllPosts("", 0, 0)
	if len(posts) == 0 {
		return
	}

	for _, post := range posts {
		if strings.TrimSpace(post.Content) == "" {
			continue
		}
		if strings.TrimSpace(post.TranslatedContent) != "" && strings.TrimSpace(post.TranslationError) == "" {
			continue
		}
		enrichPostTranslation(cfg, &post, true)
		if _, err := store.UpdatePostTranslation(post.ID, post.TranslatedContent, post.TranslatedAt, post.TranslationError); err != nil {
			continue
		}
	}
}
