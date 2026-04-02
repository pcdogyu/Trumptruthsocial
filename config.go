package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

const configFileName = "config.yaml"

var configMu sync.Mutex

func defaultConfig() Config {
	return Config{
		AccountsToMonitor: []string{},
		Auth: AuthConfig{
			BearerToken:         "YOUR_TRUTHSOCIAL_BEARER_TOKEN",
			TruthSocialUsername: "YOUR_TRUTHSOCIAL_USERNAME",
		},
		Telegram: TelegramConfig{
			BotToken: "YOUR_TELEGRAM_BOT_TOKEN",
			ChatID:   "YOUR_TELEGRAM_CHAT_ID",
		},
		AIAnalysis: AIAnalysisConfig{
			Enabled: false,
			APIKey:  "YOUR_AI_API_KEY",
			Prompt:  "Summarize the main point of the following text in one short sentence:",
		},
		RefreshInterval: "5m",
		Selectors: map[string]string{
			"post_container":           "div.status[data-id], article[data-id], div[data-id]",
			"post_id_attribute":        "data-id",
			"post_content_div":         "div.status__content",
			"post_web_url_anchor":      "a.status__relative-time",
			"video_container_div":      "div.media-gallery__item-video-container",
			"video_tag":                "video",
			"video_source_tag":         "source",
			"post_header_to_remove":    "div.status__header",
			"post_footer_to_remove":    "div.status__action-bar",
			"show_more_button":         ".status__content__read-more-button, button.read-more-button",
			"post_timestamp_tag":       "time",
			"post_timestamp_attribute": "datetime",
		},
	}
}

func LoadConfig() (Config, error) {
	configMu.Lock()
	defer configMu.Unlock()

	cfg := defaultConfig()
	file, err := os.Open(configFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	section := ""
	listMode := false

	for scanner.Scan() {
		raw := scanner.Text()
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if indent == 0 {
			listMode = false
			if strings.HasSuffix(trimmed, ":") {
				section = strings.TrimSuffix(trimmed, ":")
				if section == "accounts_to_monitor" {
					listMode = true
				}
				continue
			}

			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				continue
			}
			applyTopLevelValue(&cfg, key, value)
			continue
		}

		if section == "accounts_to_monitor" && listMode && strings.HasPrefix(trimmed, "-") {
			entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			entry = unquote(entry)
			if entry != "" {
				cfg.AccountsToMonitor = append(cfg.AccountsToMonitor, entry)
			}
			continue
		}

		if indent >= 2 {
			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				continue
			}
			applySectionValue(&cfg, section, key, value)
		}
	}

	if err := scanner.Err(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func SaveConfig(cfg Config) error {
	configMu.Lock()
	defer configMu.Unlock()

	var b strings.Builder
	b.WriteString("# TruthSocial Monitor Configuration\n\n")
	b.WriteString("accounts_to_monitor:\n")
	for _, item := range cfg.AccountsToMonitor {
		if strings.TrimSpace(item) == "" {
			continue
		}
		b.WriteString("  - ")
		b.WriteString(quoteIfNeeded(item))
		b.WriteString("\n")
	}
	if len(cfg.AccountsToMonitor) == 0 {
		b.WriteString("  []\n")
	}
	b.WriteString("\n")

	b.WriteString("auth:\n")
	b.WriteString("  bearer_token: ")
	b.WriteString(quoteIfNeeded(cfg.Auth.BearerToken))
	b.WriteString("\n")
	b.WriteString("  truthsocial_username: ")
	b.WriteString(quoteIfNeeded(cfg.Auth.TruthSocialUsername))
	b.WriteString("\n\n")

	b.WriteString("telegram:\n")
	b.WriteString("  bot_token: ")
	b.WriteString(quoteIfNeeded(cfg.Telegram.BotToken))
	b.WriteString("\n")
	b.WriteString("  chat_id: ")
	b.WriteString(quoteIfNeeded(cfg.Telegram.ChatID))
	b.WriteString("\n\n")

	b.WriteString("ai_analysis:\n")
	b.WriteString("  enabled: ")
	if cfg.AIAnalysis.Enabled {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString("\n")
	b.WriteString("  api_key: ")
	b.WriteString(quoteIfNeeded(cfg.AIAnalysis.APIKey))
	b.WriteString("\n")
	b.WriteString("  prompt: ")
	b.WriteString(quoteIfNeeded(cfg.AIAnalysis.Prompt))
	b.WriteString("\n\n")

	b.WriteString("refresh_interval: ")
	b.WriteString(quoteIfNeeded(cfg.RefreshInterval))
	b.WriteString("\n\n")

	b.WriteString("selectors:\n")
	keys := []string{
		"post_container",
		"post_id_attribute",
		"post_content_div",
		"post_web_url_anchor",
		"video_container_div",
		"video_tag",
		"video_source_tag",
		"post_header_to_remove",
		"post_footer_to_remove",
		"show_more_button",
		"post_timestamp_tag",
		"post_timestamp_attribute",
	}
	for _, key := range keys {
		if val, ok := cfg.Selectors[key]; ok {
			b.WriteString("  ")
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(quoteIfNeeded(val))
			b.WriteString("\n")
		}
	}

	return os.WriteFile(configFileName, []byte(b.String()), 0644)
}

func applyTopLevelValue(cfg *Config, key, value string) {
	switch key {
	case "refresh_interval":
		cfg.RefreshInterval = unquote(value)
	}
}

func applySectionValue(cfg *Config, section, key, value string) {
	switch section {
	case "auth":
		switch key {
		case "bearer_token":
			cfg.Auth.BearerToken = unquote(value)
		case "truthsocial_username":
			cfg.Auth.TruthSocialUsername = unquote(value)
		}
	case "telegram":
		switch key {
		case "bot_token":
			cfg.Telegram.BotToken = unquote(value)
		case "chat_id":
			cfg.Telegram.ChatID = unquote(value)
		}
	case "ai_analysis":
		switch key {
		case "enabled":
			cfg.AIAnalysis.Enabled = parseBool(value)
		case "api_key":
			cfg.AIAnalysis.APIKey = unquote(value)
		case "prompt":
			cfg.AIAnalysis.Prompt = unquote(value)
		}
	case "selectors":
		if cfg.Selectors == nil {
			cfg.Selectors = map[string]string{}
		}
		cfg.Selectors[key] = unquote(value)
	}
}

func parseBool(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "true" || s == "yes" || s == "on" || s == "1"
}

func splitKeyValue(s string) (string, string, bool) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(s[:idx])
	value := strings.TrimSpace(s[idx+1:])
	return key, value, true
}

func stripComment(line string) string {
	inSingle := false
	inDouble := false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimRight(line[:i], " \t")
			}
		}
	}
	return line
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			unquoted, err := strconv.Unquote(value)
			if err == nil {
				return unquoted
			}
			return value[1 : len(value)-1]
		}
	}
	return value
}

func quoteIfNeeded(value string) string {
	if value == "" {
		return "\"\""
	}
	if strings.ContainsAny(value, ":#\n\r\t") || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return strconv.Quote(value)
	}
	return value
}
