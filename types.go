package main

import "time"

type AuthConfig struct {
	BearerToken         string `json:"bearer_token"`
	TruthSocialUsername string `json:"truthsocial_username"`
}

type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type AIAnalysisConfig struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"api_key"`
	Prompt  string `json:"prompt"`
}

type Config struct {
	AccountsToMonitor []string          `json:"accounts_to_monitor"`
	Auth              AuthConfig        `json:"auth"`
	Telegram          TelegramConfig    `json:"telegram"`
	AIAnalysis        AIAnalysisConfig  `json:"ai_analysis"`
	RefreshInterval   string            `json:"refresh_interval"`
	Selectors         map[string]string `json:"selectors"`
}

type Post struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	Content        string `json:"content"`
	WebURL         string `json:"web_url"`
	VideoURL       string `json:"video_url"`
	Timestamp      string `json:"timestamp"`
	Status         string `json:"status"`
	RawData        string `json:"raw_data,omitempty"`
	SentToTelegram bool   `json:"sent_to_telegram"`
}

type GitInfo struct {
	Time   string
	Hash   string
	Branch string
}

type IndexPageData struct {
	Title            string
	ActivePage       string
	Git              GitInfo
	Message          string
	Config           Config
	AccountsText     string
	BearerTokenValue string
	AIApiKeyValue    string
}

type ContentPageData struct {
	Title            string
	ActivePage       string
	Git              GitInfo
	Posts            []Post
	Usernames        []string
	SelectedUsername string
}

type MessagePushPageData struct {
	Title      string
	ActivePage string
	Git        GitInfo
	BotToken   string
	ChatID     string
}

type AIConfigPageData struct {
	Title         string
	ActivePage    string
	Git           GitInfo
	Config        Config
	AIApiKeyValue string
}

type SyncResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	NewPosts int    `json:"new_posts,omitempty"`
}

type SyncRequest struct {
	Days int `json:"days"`
}

type BulkActionRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
}

const (
	PostStatusNormal  = "normal"
	PostStatusBlocked = "blocked"
)

func normalizeTimeString(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
