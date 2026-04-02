package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const configFileName = "config.yaml"
const defaultBearerTokenValidityDays = 5

var configMu sync.Mutex

func defaultConfig() Config {
	return Config{
		AccountsToMonitor: []string{},
		Auth: AuthConfig{
			BearerToken:             "YOUR_TRUTHSOCIAL_BEARER_TOKEN",
			BearerTokenValidityDays: defaultBearerTokenValidityDays,
			TruthSocialUsername:     "YOUR_TRUTHSOCIAL_USERNAME",
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
		Translation: TranslationConfig{
			Enabled:        false,
			APIURL:         "YOUR_TRANSLATION_API_URL",
			APIKey:         "YOUR_TRANSLATION_API_KEY",
			Model:          "YOUR_TRANSLATION_MODEL",
			SourceLanguage: "auto",
			TargetLanguage: "zh-CN",
			TimeoutSeconds: 30,
			Prompt:         defaultTranslationPrompt,
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
	b.WriteString("  bearer_token_backup_1: ")
	b.WriteString(quoteIfNeeded(cfg.Auth.BearerTokenBackup1))
	b.WriteString("\n")
	b.WriteString("  bearer_token_backup_2: ")
	b.WriteString(quoteIfNeeded(cfg.Auth.BearerTokenBackup2))
	b.WriteString("\n")
	b.WriteString("  bearer_token_updated_at: ")
	b.WriteString(quoteIfNeeded(cfg.Auth.BearerTokenUpdatedAt))
	b.WriteString("\n")
	b.WriteString("  bearer_token_validity_days: ")
	b.WriteString(strconv.Itoa(validBearerTokenValidityDays(cfg.Auth.BearerTokenValidityDays)))
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

	b.WriteString("translation:\n")
	b.WriteString("  enabled: ")
	if cfg.Translation.Enabled {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString("\n")
	b.WriteString("  api_url: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.APIURL))
	b.WriteString("\n")
	b.WriteString("  api_key: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.APIKey))
	b.WriteString("\n")
	b.WriteString("  model: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.Model))
	b.WriteString("\n")
	b.WriteString("  source_language: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.SourceLanguage))
	b.WriteString("\n")
	b.WriteString("  target_language: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.TargetLanguage))
	b.WriteString("\n")
	b.WriteString("  timeout_seconds: ")
	b.WriteString(strconv.Itoa(cfg.Translation.TimeoutSeconds))
	b.WriteString("\n")
	b.WriteString("  prompt: ")
	b.WriteString(quoteIfNeeded(cfg.Translation.Prompt))
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
		case "bearer_token_backup_1":
			cfg.Auth.BearerTokenBackup1 = unquote(value)
		case "bearer_token_backup_2":
			cfg.Auth.BearerTokenBackup2 = unquote(value)
		case "bearer_token_updated_at":
			cfg.Auth.BearerTokenUpdatedAt = unquote(value)
		case "bearer_token_validity_days":
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				cfg.Auth.BearerTokenValidityDays = n
			}
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
	case "translation":
		switch key {
		case "enabled":
			cfg.Translation.Enabled = parseBool(value)
		case "api_url":
			cfg.Translation.APIURL = unquote(value)
		case "api_key":
			cfg.Translation.APIKey = unquote(value)
		case "model":
			cfg.Translation.Model = unquote(value)
		case "source_language":
			cfg.Translation.SourceLanguage = unquote(value)
		case "target_language":
			cfg.Translation.TargetLanguage = unquote(value)
		case "timeout_seconds":
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				cfg.Translation.TimeoutSeconds = n
			}
		case "prompt":
			cfg.Translation.Prompt = unquote(value)
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

func validBearerTokenValidityDays(value int) int {
	if value <= 0 {
		return defaultBearerTokenValidityDays
	}
	return value
}

func bearerTokenPlaceholder(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}
	return strings.Contains(token, "YOUR_TRUTHSOCIAL_BEARER_TOKEN")
}

func bearerTokenCandidates(cfg Config, preferred string) []string {
	items := []string{
		strings.TrimSpace(preferred),
		strings.TrimSpace(cfg.Auth.BearerToken),
		strings.TrimSpace(cfg.Auth.BearerTokenBackup1),
		strings.TrimSpace(cfg.Auth.BearerTokenBackup2),
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if bearerTokenPlaceholder(item) {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func bearerTokenNeedsRefresh(cfg Config, now time.Time) bool {
	token := strings.TrimSpace(cfg.Auth.BearerToken)
	if bearerTokenPlaceholder(token) {
		return true
	}
	updatedAt := strings.TrimSpace(cfg.Auth.BearerTokenUpdatedAt)
	if updatedAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return false
	}
	validityDays := validBearerTokenValidityDays(cfg.Auth.BearerTokenValidityDays)
	if validityDays <= 0 {
		return false
	}
	expiry := parsed.Add(time.Duration(validityDays) * 24 * time.Hour)
	return !now.Before(expiry)
}

func rotateBearerTokens(cfg *Config, newToken string) {
	newToken = strings.TrimSpace(newToken)
	if newToken == "" {
		cfg.Auth.BearerToken = ""
		cfg.Auth.BearerTokenBackup1 = ""
		cfg.Auth.BearerTokenBackup2 = ""
		cfg.Auth.BearerTokenUpdatedAt = ""
		return
	}

	items := []string{
		newToken,
		strings.TrimSpace(cfg.Auth.BearerToken),
		strings.TrimSpace(cfg.Auth.BearerTokenBackup1),
		strings.TrimSpace(cfg.Auth.BearerTokenBackup2),
	}
	seen := map[string]struct{}{}
	rotated := make([]string, 0, 3)
	for _, item := range items {
		if bearerTokenPlaceholder(item) {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		rotated = append(rotated, item)
		if len(rotated) == 3 {
			break
		}
	}

	cfg.Auth.BearerToken = ""
	cfg.Auth.BearerTokenBackup1 = ""
	cfg.Auth.BearerTokenBackup2 = ""
	if len(rotated) > 0 {
		cfg.Auth.BearerToken = rotated[0]
	}
	if len(rotated) > 1 {
		cfg.Auth.BearerTokenBackup1 = rotated[1]
	}
	if len(rotated) > 2 {
		cfg.Auth.BearerTokenBackup2 = rotated[2]
	}
	cfg.Auth.BearerTokenUpdatedAt = time.Now().UTC().Format(time.RFC3339)
}
