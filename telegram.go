package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

func forwardPostToTelegram(cfg Config, post Post) (bool, string) {
	text := fmt.Sprintf("<b>来自: @%s</b>\n\n", html.EscapeString(post.Username))
	if strings.TrimSpace(post.Content) != "" {
		text += html.EscapeString(post.Content) + "\n\n"
	}
	if strings.TrimSpace(post.VideoURL) != "" {
		text += "视频链接: " + html.EscapeString(post.VideoURL) + "\n\n"
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
	botToken := strings.TrimSpace(cfg.Telegram.BotToken)
	chatID := strings.TrimSpace(cfg.Telegram.ChatID)
	if botToken == "" || chatID == "" || strings.Contains(botToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return false, "Telegram 未在 config.yaml 中正确配置。"
	}

	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err.Error()
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken), bytes.NewReader(body))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("Telegram API HTTP 状态码: %s", resp.Status)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err.Error()
	}
	if ok, _ := result["ok"].(bool); ok {
		return true, "帖子已成功转发到 Telegram。"
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
		if err == nil && resp != nil {
			var result map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&result)
			_ = resp.Body.Close()
			if ok, _ := result["ok"].(bool); ok {
				return true, "成功发送视频到 Telegram。"
			}
		}
		if attempt < 2 {
			time.Sleep(3 * time.Second)
		}
	}
	return false, "发送 Telegram 视频失败。"
}
