package main

import (
	"log"
	"strings"
	"time"
)

const telegramSendGap = 8 * time.Second
const historicalTelegramSendGap = 20 * time.Second

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
	if !hasConfiguredBearerToken(cfg) {
		log.Printf("Bearer Token 为空或仍是占位值，跳过历史同步。")
		return 0, nil
	}

	cutoff := time.Time{}
	if days > 0 {
		cutoff = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	}
	debugf("syncAllAccounts start: days=%d accounts=%d cutoff=%s", days, len(cfg.AccountsToMonitor), cutoff.Format(time.RFC3339))

	added := 0
	for _, entry := range cfg.AccountsToMonitor {
		profileURL := normalizeProfileURL(entry)
		if profileURL == "" {
			continue
		}
		debugf("historical sync fetching account=%s", profileURL)

		posts, err := fetchHistoricalPosts(profileURL, cfg, cutoff)
		if err != nil {
			log.Printf("fetch historical posts failed for %s: %v", profileURL, err)
			continue
		}
		debugf("historical sync fetched account=%s posts=%d", profileURL, len(posts))
		for _, post := range posts {
			hydratePostTranslationFromStore(store, &post)
			enrichPostTranslation(cfg, &post, false)
			ok, err := store.UpsertPost(post)
			if err != nil {
				log.Printf("store upsert post failed: %v", err)
				continue
			}
			if ok {
				added++
			}
		}
	}
	forwardUnsentPostsWithGap(store, cfg, historicalTelegramSendGapForDays(days))
	return added, nil
}

func syncLatestAccounts(store *PostStore) (int, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return 0, err
	}
	if !hasConfiguredBearerToken(cfg) {
		log.Printf("Bearer Token 为空或仍是占位值，跳过最新内容同步。")
		return 0, nil
	}
	debugf("syncLatestAccounts start: accounts=%d", len(cfg.AccountsToMonitor))

	added := 0
	latestPosts := make([]Post, 0, len(cfg.AccountsToMonitor))
	for _, entry := range cfg.AccountsToMonitor {
		profileURL := normalizeProfileURL(entry)
		if profileURL == "" {
			continue
		}
		debugf("latest sync fetching account=%s", profileURL)

		posts, err := fetchLatestPostsWithLimit(profileURL, cfg, 1)
		if err != nil {
			log.Printf("fetch latest post failed for %s: %v", profileURL, err)
			continue
		}
		debugf("latest sync fetched account=%s posts=%d", profileURL, len(posts))
		if len(posts) == 0 {
			continue
		}
		post := posts[0]
		hydratePostTranslationFromStore(store, &post)
		enrichPostTranslation(cfg, &post, false)
		ok, err := store.UpsertPost(post)
		if err != nil {
			log.Printf("store upsert latest post failed: %v", err)
			continue
		}
		if ok {
			added++
		}
		if stored, exists := store.GetPostByID(post.ID); exists && stored.Status != PostStatusBlocked && !stored.SentToTelegram {
			latestPosts = append(latestPosts, stored)
		}
	}
	forwardPostsWithGap(cfg, latestPosts, telegramSendGap)
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
		if !hasConfiguredBearerToken(cfg) {
			log.Printf("Bearer Token 为空或仍是占位值，跳过本轮监控并休眠 %s", interval)
			time.Sleep(interval)
			continue
		}
		debugf("monitor cycle start: interval=%s accounts=%d", interval, len(cfg.AccountsToMonitor))

		for _, entry := range cfg.AccountsToMonitor {
			profileURL := normalizeProfileURL(entry)
			if profileURL == "" {
				continue
			}
			debugf("monitor fetching latest account=%s", profileURL)

			posts, err := fetchLatestPosts(profileURL, cfg)
			if err != nil {
				log.Printf("fetch latest posts failed for %s: %v", profileURL, err)
				continue
			}
			for _, post := range posts {
				hydratePostTranslationFromStore(store, &post)
				enrichPostTranslation(cfg, &post, false)
				if _, err := store.UpsertPost(post); err != nil {
					log.Printf("upsert post failed for %s: %v", post.ID, err)
				}
			}
		}
		forwardUnsentPostsWithGap(store, cfg, telegramSendGap)

		log.Printf("监控周期结束，休眠 %s", interval)
		time.Sleep(interval)
	}
}

func forwardUnsentPosts(store *PostStore, cfg Config) {
	forwardUnsentPostsWithGap(store, cfg, telegramSendGap)
}

func forwardPostsWithGap(cfg Config, posts []Post, gap time.Duration) {
	if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" || strings.Contains(cfg.Telegram.BotToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return
	}

	if gap <= 0 {
		gap = telegramSendGap
	}

	for _, post := range posts {
		debugf("forwarding selected post id=%s username=@%s has_image=%t has_video=%t", post.ID, post.Username, strings.TrimSpace(post.ImageURL) != "", strings.TrimSpace(post.VideoURL) != "")
		ok, message := forwardPostToTelegram(cfg, post)
		if !ok {
			log.Printf("telegram send failed for %s: %s", post.ID, message)
		}
		time.Sleep(gap)
	}
}

func historicalTelegramSendGapForDays(days int) time.Duration {
	switch {
	case days >= 30:
		return 30 * time.Second
	case days >= 14:
		return 25 * time.Second
	case days >= 7:
		return historicalTelegramSendGap
	default:
		return telegramSendGap
	}
}

func forwardUnsentPostsWithGap(store *PostStore, cfg Config, gap time.Duration) {
	if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" || strings.Contains(cfg.Telegram.BotToken, "YOUR_TELEGRAM_BOT_TOKEN") {
		return
	}

	if gap <= 0 {
		gap = telegramSendGap
	}

	unsent := store.GetUnsentPosts()
	debugf("forwardUnsentPosts start: count=%d gap=%s", len(unsent), gap)
	for _, post := range unsent {
		debugf("forwarding post id=%s username=@%s has_image=%t has_video=%t", post.ID, post.Username, strings.TrimSpace(post.ImageURL) != "", strings.TrimSpace(post.VideoURL) != "")
		ok, message := forwardPostToTelegram(cfg, post)
		if ok {
			if _, err := store.MarkSent(post.ID); err != nil {
				log.Printf("mark sent failed for %s: %v", post.ID, err)
			}
		} else {
			log.Printf("telegram send failed for %s: %s", post.ID, message)
		}
		time.Sleep(gap)
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

func buildMediaFallbackText(post Post) string {
	return buildTelegramHTMLPost(post, 0, true)
}

func buildVideoFallbackText(post Post) string {
	return buildMediaFallbackText(post)
}
