package main

import (
	"log"
	"strings"
	"time"
)

const telegramSendGap = 8 * time.Second

func parseDuration(interval string) time.Duration {
	interval = strings.TrimSpace(interval)
	if interval == "" {
		return 5 * time.Minute
	}
	if d, err := time.ParseDuration(interval); err == nil {
		return d
	}
	unit := interval[len(interval)-1]
	value := interval[:len(interval)-1]
	switch unit {
	case 's', 'S':
		if d, err := time.ParseDuration(value + "s"); err == nil {
			return d
		}
	case 'm', 'M':
		if d, err := time.ParseDuration(value + "m"); err == nil {
			return d
		}
	case 'h', 'H':
		if d, err := time.ParseDuration(value + "h"); err == nil {
			return d
		}
	}
	return 5 * time.Minute
}

func syncAllAccounts(store *PostStore, days int) (int, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return 0, err
	}

	cutoff := time.Time{}
	if days > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	}

	added := 0
	for _, entry := range cfg.AccountsToMonitor {
		profileURL := normalizeProfileURL(entry)
		if profileURL == "" {
			continue
		}

		posts, err := fetchLatestPosts(profileURL, cfg)
		if err != nil {
			log.Printf("fetch latest posts failed for %s: %v", profileURL, err)
			continue
		}
		for _, post := range posts {
			if days > 0 {
				if t := parsePostTime(post.Timestamp); !t.IsZero() && t.Before(cutoff) {
					continue
				}
			}
			ok, err := store.AddPost(post)
			if err != nil {
				log.Printf("store add post failed: %v", err)
				continue
			}
			if ok {
				added++
			}
		}
	}
	forwardUnsentPosts(store, cfg)
	return added, nil
}

func syncLatestAccounts(store *PostStore) (int, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return 0, err
	}

	added := 0
	for _, entry := range cfg.AccountsToMonitor {
		profileURL := normalizeProfileURL(entry)
		if profileURL == "" {
			continue
		}

		posts, err := fetchLatestPostsWithLimit(profileURL, cfg, 1)
		if err != nil {
			log.Printf("fetch latest post failed for %s: %v", profileURL, err)
			continue
		}
		if len(posts) == 0 {
			continue
		}
		ok, err := store.AddPost(posts[0])
		if err != nil {
			log.Printf("store add latest post failed: %v", err)
			continue
		}
		if ok {
			added++
		}
	}
	forwardUnsentPosts(store, cfg)
	return added, nil
}

func runMonitor(store *PostStore) {
	for {
		cfg, err := LoadConfig()
		if err != nil {
			log.Printf("load config failed: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}

		interval := parseDuration(cfg.RefreshInterval)
		if interval <= 0 {
			interval = 5 * time.Minute
		}

		if len(cfg.AccountsToMonitor) == 0 {
			log.Println("监控列表为空。")
		}

		for _, entry := range cfg.AccountsToMonitor {
			profileURL := normalizeProfileURL(entry)
			if profileURL == "" {
				continue
			}

			posts, err := fetchLatestPosts(profileURL, cfg)
			if err != nil {
				log.Printf("fetch latest posts failed for %s: %v", profileURL, err)
				continue
			}
			for _, post := range posts {
				if _, err := store.AddPost(post); err != nil {
					log.Printf("add post failed for %s: %v", post.ID, err)
				}
			}
		}
		forwardUnsentPosts(store, cfg)

		log.Printf("监控周期结束，休眠 %s", interval)
		time.Sleep(interval)
	}
}

func forwardUnsentPosts(store *PostStore, cfg Config) {
	if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" || strings.Contains(cfg.Telegram.BotToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return
	}

	unsent := store.GetUnsentPosts()
	for _, post := range unsent {
		var ok bool
		var message string
		if strings.TrimSpace(post.VideoURL) != "" {
			caption := "<b>" + post.Username + " 发布了新视频:</b>\n\n" + htmlEscape(post.Content) + "\n\n<a href='" + htmlEscape(post.WebURL) + "'>查看原文</a>"
			ok, message = sendTelegramVideo(cfg, post.VideoURL, caption)
			if !ok {
				log.Printf("telegram video send failed for %s: %s, falling back to text", post.ID, message)
				ok, message = sendTelegramHTMLMessage(cfg, buildVideoFallbackText(post))
			}
		} else {
			ok, message = forwardPostToTelegram(cfg, post)
		}
		if ok {
			if _, err := store.MarkSent(post.ID); err != nil {
				log.Printf("mark sent failed for %s: %v", post.ID, err)
			}
		} else {
			log.Printf("telegram send failed for %s: %s", post.ID, message)
		}
		time.Sleep(telegramSendGap)
	}
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"'", "&#39;",
		"\"", "&quot;",
	)
	return replacer.Replace(value)
}

func buildVideoFallbackText(post Post) string {
	text := "<b>来自: @" + htmlEscape(post.Username) + "</b>\n\n"
	if strings.TrimSpace(post.Content) != "" {
		text += htmlEscape(post.Content) + "\n\n"
	}
	if strings.TrimSpace(post.VideoURL) != "" {
		text += htmlEscape(post.VideoURL) + "\n\n"
	}
	if strings.TrimSpace(post.WebURL) != "" {
		text += "<a href='" + htmlEscape(post.WebURL) + "'>查看原文</a>"
	}
	return text
}
