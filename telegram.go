package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var youtubeURLRe = regexp.MustCompile(`(?i)https?://(?:www\.|m\.)?(?:youtube\.com/watch\?v=[^\s<]+|youtu\.be/[^\s<]+)`)

func forwardPostToTelegram(cfg Config, post Post) (bool, string) {
	youtubeURL := extractYouTubeURL(post.Content)
	if youtubeURL == "" {
		youtubeURL = extractYouTubeURL(post.VideoURL)
	}
	if youtubeURL != "" {
		text := buildYouTubeTelegramText(post, youtubeURL)
		var replyMarkup any
		if strings.TrimSpace(post.WebURL) != "" {
			replyMarkup = map[string]any{
				"inline_keyboard": []any{
					[]any{
						map[string]string{
							"text": "查看原文",
							"url":  post.WebURL,
						},
					},
				},
			}
		}
		return sendTelegramPlainMessageWithReplyMarkup(cfg, text, replyMarkup)
	}

	if strings.TrimSpace(post.VideoURL) != "" {
		caption := "<b>来自: @" + html.EscapeString(post.Username) + "</b>\n\n"
		if strings.TrimSpace(post.Content) != "" {
			caption += html.EscapeString(post.Content) + "\n\n"
		}
		if strings.TrimSpace(post.WebURL) != "" {
			caption += "<a href='" + html.EscapeString(post.WebURL) + "'>查看原文</a>"
		}
		return sendTelegramVideo(cfg, post.VideoURL, caption)
	}

	text := fmt.Sprintf("<b>来自: @%s</b>\n\n", html.EscapeString(post.Username))
	if strings.TrimSpace(post.Content) != "" {
		text += html.EscapeString(post.Content) + "\n\n"
	}
	if strings.TrimSpace(post.WebURL) != "" {
		text += fmt.Sprintf("<a href='%s'>查看原文</a>", html.EscapeString(post.WebURL))
	}
	return sendTelegramHTMLMessage(cfg, text)
}

func sendTelegramTestMessage(cfg Config) (bool, string) {
	return sendTelegramHTMLMessage(cfg, "这是一条来自 TruthSocial Monitor 的测试消息。")
}

func sendTelegramHTMLMessage(cfg Config, text string) (bool, string) {
	return sendTelegramHTMLMessageWithReplyMarkup(cfg, text, nil)
}

func sendTelegramHTMLMessageWithReplyMarkup(cfg Config, text string, replyMarkup any) (bool, string) {
	return sendTelegramMessageWithReplyMarkup(cfg, text, "HTML", replyMarkup)
}

func sendTelegramPlainMessageWithReplyMarkup(cfg Config, text string, replyMarkup any) (bool, string) {
	return sendTelegramMessageWithReplyMarkup(cfg, text, "", replyMarkup)
}

func sendTelegramMessageWithReplyMarkup(cfg Config, text, parseMode string, replyMarkup any) (bool, string) {
	botToken := strings.TrimSpace(cfg.Telegram.BotToken)
	chatID := strings.TrimSpace(cfg.Telegram.ChatID)
	if botToken == "" || chatID == "" || strings.Contains(botToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return false, "Telegram 未在 config.yaml 中正确配置。"
	}

	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": false,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err.Error()
	}

	client := &http.Client{Timeout: 15 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken), bytes.NewReader(body))
		if err != nil {
			return false, err.Error()
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if attempt < 2 {
				time.Sleep(telegramSendGap)
				continue
			}
			return false, err.Error()
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return false, readErr.Error()
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := telegramRetryAfter(respBody)
			if retryAfter <= 0 {
				retryAfter = telegramSendGap
			}
			log.Printf("Telegram rate limited for sendMessage, waiting %s", retryAfter)
			if attempt < 2 {
				time.Sleep(retryAfter)
				continue
			}
			return false, telegramHTTPErrorMessage(resp.Status, respBody)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return false, telegramHTTPErrorMessage(resp.Status, respBody)
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			return false, err.Error()
		}
		if ok, _ := result["ok"].(bool); ok {
			return true, "帖子已成功转发到 Telegram。"
		}
		return false, "Telegram API 返回失败。"
	}
	return false, "Telegram API 返回失败。"
}

func sendTelegramVideo(cfg Config, videoURL, caption string) (bool, string) {
	botToken := strings.TrimSpace(cfg.Telegram.BotToken)
	chatID := strings.TrimSpace(cfg.Telegram.ChatID)
	if botToken == "" || chatID == "" || strings.Contains(botToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return false, "Telegram 未在 config.yaml 中正确配置。"
	}

	payload := map[string]string{
		"chat_id":    chatID,
		"video":      videoURL,
		"caption":    caption,
		"parse_mode": "HTML",
	}

	client := &http.Client{Timeout: 60 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		body, err := json.Marshal(payload)
		if err != nil {
			return false, err.Error()
		}
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendVideo", botToken), bytes.NewReader(body))
		if err != nil {
			return false, err.Error()
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if attempt < 2 {
				time.Sleep(telegramSendGap)
				continue
			}
			return false, err.Error()
		}
		if resp == nil {
			if attempt < 2 {
				time.Sleep(telegramSendGap)
				continue
			}
			return false, "Telegram 视频请求失败。"
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return false, readErr.Error()
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := telegramRetryAfter(respBody)
			if retryAfter <= 0 {
				retryAfter = telegramSendGap
			}
			log.Printf("Telegram rate limited for sendVideo, waiting %s", retryAfter)
			if attempt < 2 {
				time.Sleep(retryAfter)
				continue
			}
			return false, telegramHTTPErrorMessage(resp.Status, respBody)
		}

		if resp.StatusCode == http.StatusBadRequest {
			return false, telegramHTTPErrorMessage(resp.Status, respBody)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt < 2 {
				time.Sleep(telegramSendGap)
				continue
			}
			return false, telegramHTTPErrorMessage(resp.Status, respBody)
		}

		var result map[string]any
		if err := json.Unmarshal(respBody, &result); err != nil {
			return false, err.Error()
		}
		if ok, _ := result["ok"].(bool); ok {
			return true, "成功发送视频到 Telegram。"
		}
		if attempt < 2 {
			time.Sleep(telegramSendGap)
		}
	}
	return false, "发送 Telegram 视频失败。"
}

func telegramRetryAfter(body []byte) time.Duration {
	if len(body) == 0 {
		return 0
	}

	var result struct {
		ErrorCode  int    `json:"error_code"`
		OK         bool   `json:"ok"`
		Desc       string `json:"description"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}
	if result.ErrorCode == http.StatusTooManyRequests && result.Parameters.RetryAfter > 0 {
		return time.Duration(result.Parameters.RetryAfter) * time.Second
	}
	return 0
}

func telegramHTTPErrorMessage(status string, body []byte) string {
	var result struct {
		Description string `json:"description"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err == nil && strings.TrimSpace(result.Description) != "" {
			return fmt.Sprintf("Telegram API HTTP 状态码: %s, 详情: %s", status, result.Description)
		}
	}
	return fmt.Sprintf("Telegram API HTTP 状态码: %s", status)
}

func extractYouTubeURL(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	compact := strings.Join(strings.Fields(text), "")
	match := youtubeURLRe.FindString(compact)
	if match == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(match), "https://m.youtube.com/") {
		match = "https://www.youtube.com/" + strings.TrimPrefix(match, "https://m.youtube.com/")
	} else if strings.HasPrefix(strings.ToLower(match), "http://m.youtube.com/") {
		match = "https://www.youtube.com/" + strings.TrimPrefix(match, "http://m.youtube.com/")
	}
	if strings.HasPrefix(strings.ToLower(match), "http://www.youtube.com/") {
		match = "https://www.youtube.com/" + strings.TrimPrefix(match, "http://www.youtube.com/")
	}
	return strings.TrimSpace(match)
}

func buildYouTubeTelegramText(post Post, youtubeURL string) string {
	title := cleanTelegramContent(post.Content)
	text := "来自: @" + post.Username + "\n\n"
	if title != "" {
		text += title + "\n\n"
	}
	text += youtubeURL
	return text
}

func cleanTelegramContent(content string) string {
	lines := strings.Split(content, "\n")
	parts := make([]string, 0, len(lines))
	skippingURL := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			skippingURL = false
			continue
		}
		lower := strings.ToLower(line)
		if isLikelyURLLine(lower) {
			skippingURL = true
			continue
		}
		if skippingURL && isLikelyURLContinuation(line) {
			continue
		}
		skippingURL = false
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func isLikelyURLLine(lower string) bool {
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "www.") ||
		strings.Contains(lower, "youtube.com") ||
		strings.Contains(lower, "youtu.be")
}

func isLikelyURLContinuation(line string) bool {
	if strings.Contains(line, " ") {
		return false
	}
	if len(line) < 6 {
		return false
	}
	if strings.Contains(line, ".") || strings.Contains(line, "?") || strings.Contains(line, "&") || strings.Contains(line, "=") || strings.Contains(line, "/") {
		return true
	}
	return false
}
