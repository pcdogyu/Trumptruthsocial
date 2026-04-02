package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	openTagRe      = regexp.MustCompile(`(?is)<(article|div)[^>]*data-id=["']([^"']+)["'][^>]*>`)
	timeTagRe      = regexp.MustCompile(`(?is)<time[^>]*datetime=["']([^"']+)["'][^>]*>`)
	webURLRe       = regexp.MustCompile(`(?is)<a[^>]*class=["'][^"']*status__relative-time[^"']*["'][^>]*href=["']([^"']+)["']`)
	sourceURLRe    = regexp.MustCompile(`(?is)<source[^>]*src=["']([^"']+)["']`)
	videoSrcRe     = regexp.MustCompile(`(?is)<video[^>]*src=["']([^"']+)["']`)
	contentBlockRe = regexp.MustCompile(`(?is)class=["'][^"']*status__content[^"']*["'][^>]*>(.*?)</div>`)
	tagRe          = regexp.MustCompile(`(?is)<[^>]+>`)
	scriptRe       = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe        = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
)

func normalizeProfileURL(entry string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ""
	}
	if strings.HasPrefix(entry, "http://") || strings.HasPrefix(entry, "https://") {
		return entry
	}
	entry = strings.TrimPrefix(entry, "@")
	return fmt.Sprintf("https://truthsocial.com/@%s", entry)
}

func extractUsernameFromEntry(entry string) string {
	entry = strings.TrimSpace(entry)
	if strings.HasPrefix(entry, "http://") || strings.HasPrefix(entry, "https://") {
		parts := strings.Split(strings.TrimRight(entry, "/"), "/")
		if len(parts) > 0 {
			return strings.TrimPrefix(parts[len(parts)-1], "@")
		}
	}
	return strings.TrimPrefix(entry, "@")
}

func fetchLatestPosts(profileURL string, cfg Config) ([]Post, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	if token := strings.TrimSpace(cfg.Auth.BearerToken); token != "" && !strings.Contains(token, "YOUR_TRUTHSOCIAL_BEARER_TOKEN") {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	page := string(body)
	username := extractUsernameFromEntry(profileURL)
	if username == "" {
		username = "unknown"
	}

	opens := openTagRe.FindAllStringSubmatchIndex(page, -1)
	if len(opens) == 0 {
		return []Post{}, nil
	}

	posts := make([]Post, 0, len(opens))
	for i, match := range opens {
		idStart := match[4]
		idEnd := match[5]
		postID := page[idStart:idEnd]
		if postID == "" {
			continue
		}

		openEnd := match[1]
		sliceEnd := len(page)
		if i+1 < len(opens) {
			sliceEnd = opens[i+1][0]
		}
		if openEnd >= sliceEnd {
			continue
		}

		block := removeScriptStyle(page[openEnd:sliceEnd])

		timestamp := ""
		if tm := timeTagRe.FindStringSubmatch(block); len(tm) > 1 {
			timestamp = normalizePostTimestamp(tm[1])
		}

		webURL := ""
		if wm := webURLRe.FindStringSubmatch(block); len(wm) > 1 {
			webURL = html.UnescapeString(wm[1])
			if strings.HasPrefix(webURL, "/") {
				webURL = "https://truthsocial.com" + webURL
			}
		}
		if webURL == "" {
			webURL = fmt.Sprintf("https://truthsocial.com/@%s/posts/%s", username, postID)
		}

		videoURL := ""
		if vm := sourceURLRe.FindStringSubmatch(block); len(vm) > 1 {
			videoURL = html.UnescapeString(vm[1])
		} else if vm := videoSrcRe.FindStringSubmatch(block); len(vm) > 1 {
			videoURL = html.UnescapeString(vm[1])
		}

		posts = append(posts, Post{
			ID:        postID,
			Username:  username,
			Content:   extractContent(block),
			WebURL:    webURL,
			VideoURL:  videoURL,
			Timestamp: timestamp,
		})
	}

	return posts, nil
}

func extractContent(block string) string {
	content := ""
	if m := contentBlockRe.FindStringSubmatch(block); len(m) > 1 {
		content = m[1]
	} else {
		content = block
	}

	content = removeScriptStyle(content)
	content = tagRe.ReplaceAllString(content, "\n")
	content = html.UnescapeString(content)

	lines := strings.Split(content, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "查看原文") {
			continue
		}
		if strings.HasPrefix(line, "Post ID:") {
			continue
		}
		parts = append(parts, line)
	}

	joined := strings.Join(parts, "\n")
	joined = strings.TrimSpace(joined)
	if len(joined) > 1200 {
		joined = joined[:1200] + "..."
	}
	return joined
}

func normalizePostTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, "Z") {
		if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return value
}

func removeScriptStyle(s string) string {
	s = scriptRe.ReplaceAllString(s, "")
	s = styleRe.ReplaceAllString(s, "")
	return s
}
