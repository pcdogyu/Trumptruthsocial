package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

var (
	openTagRe      = regexp.MustCompile(`(?is)<(article|div)[^>]*data-id=["']([^"']+)["'][^>]*>`)
	openPostRe     = regexp.MustCompile(`(?is)<(article|div)[^>]*data-testid=["']([^"']*post[^"']*)["'][^>]*>`)
	timeTagRe      = regexp.MustCompile(`(?is)<time[^>]*datetime=["']([^"']+)["'][^>]*>`)
	webURLRe       = regexp.MustCompile(`(?is)<a[^>]*class=["'][^"']*status__relative-time[^"']*["'][^>]*href=["']([^"']+)["']`)
	statusURLRe    = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']*/status/([^"'/?#]+)[^"']*)["']`)
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
	return fetchLatestPostsWithLimit(profileURL, cfg, 40)
}

func fetchLatestPostsWithLimit(profileURL string, cfg Config, limit int) ([]Post, error) {
	var apiErr error
	if posts, err := fetchPostsViaBrowserAPI(profileURL, cfg, limit); err == nil && len(posts) > 0 {
		return posts, nil
	} else if err != nil {
		apiErr = err
		log.Printf("browser API fetch failed for %s: %v", profileURL, err)
	}

	page, err := fetchPageHTMLWithBrowser(profileURL)
	if err != nil {
		page, err = fetchPageHTMLWithHTTP(profileURL, cfg)
		if err != nil {
			return nil, err
		}
	}

	username := extractUsernameFromEntry(profileURL)
	if username == "" {
		username = "unknown"
	}

	if isCloudflareBlock(page) {
		return nil, fmt.Errorf("truth social returned a Cloudflare block page")
	}

	posts := parsePostsFromHTML(page, username)
	sortPostsByFreshness(posts)
	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
	}
	if len(posts) == 0 && apiErr != nil {
		return nil, apiErr
	}
	return posts, nil
}

func parsePostsFromHTML(page, username string) []Post {
	opens := mergePostOpenings(page)
	if len(opens) == 0 {
		return []Post{}
	}

	posts := make([]Post, 0, len(opens))
	for i, opening := range opens {
		openEnd := opening.end
		sliceEnd := len(page)
		if i+1 < len(opens) {
			sliceEnd = opens[i+1].start
		}
		if openEnd >= sliceEnd {
			continue
		}

		block := removeScriptStyle(page[openEnd:sliceEnd])
		postID := extractPostID(page, block, opening)
		if postID == "" {
			continue
		}

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
		} else if wm := statusURLRe.FindStringSubmatch(block); len(wm) > 2 {
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
			Status:    PostStatusNormal,
		})
	}

	return posts
}

type postOpening struct {
	start int
	end   int
	match []int
}

func mergePostOpenings(page string) []postOpening {
	opens := make([]postOpening, 0)
	for _, match := range openTagRe.FindAllStringSubmatchIndex(page, -1) {
		opens = append(opens, postOpening{start: match[0], end: match[1], match: match})
	}
	for _, match := range openPostRe.FindAllStringSubmatchIndex(page, -1) {
		opens = append(opens, postOpening{start: match[0], end: match[1], match: match})
	}
	if len(opens) == 0 {
		return nil
	}
	sort.Slice(opens, func(i, j int) bool {
		if opens[i].start == opens[j].start {
			return opens[i].end < opens[j].end
		}
		return opens[i].start < opens[j].start
	})
	dedup := opens[:0]
	lastStart := -1
	for _, opening := range opens {
		if opening.start == lastStart {
			continue
		}
		dedup = append(dedup, opening)
		lastStart = opening.start
	}
	return dedup
}

func extractPostID(source, block string, opening postOpening) string {
	match := opening.match
	if len(match) > 5 {
		idStart := match[4]
		idEnd := match[5]
		if idStart >= 0 && idEnd >= idStart && idEnd <= len(source) {
			if postID := source[idStart:idEnd]; postID != "" {
				return postID
			}
		}
	}
	if len(match) > 3 {
		testIDStart := match[2]
		testIDEnd := match[3]
		if testIDStart >= 0 && testIDEnd >= testIDStart && testIDEnd <= len(source) {
			testID := source[testIDStart:testIDEnd]
			if strings.Contains(strings.ToLower(testID), "post") {
				if wm := statusURLRe.FindStringSubmatch(block); len(wm) > 2 {
					return wm[2]
				}
			}
		}
	}
	if wm := statusURLRe.FindStringSubmatch(block); len(wm) > 2 {
		return wm[2]
	}
	return ""
}

func fetchPageHTMLWithBrowser(profileURL string) (string, error) {
	chromePath, err := findChromeExecPath()
	if err != nil {
		return "", err
	}

	userDataDir, err := filepath.Abs(".chrome-profile")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return "", err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(userDataDir),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("excludeSwitches", "enable-automation"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var page string
	tasks := []chromedp.Action{
		chromedp.Navigate(profileURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitForRenderablePosts(ctx, 60*time.Second)
		}),
		chromedp.Evaluate(`window.scrollBy(0, 800);`, nil),
		chromedp.Sleep(2 * time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`document.querySelectorAll('.status__content__read-more-button, button.read-more-button').forEach(function(btn){ if (btn.offsetParent !== null) btn.click(); });`, nil).Do(ctx)
		}),
		chromedp.Sleep(1 * time.Second),
		chromedp.OuterHTML("html", &page, chromedp.ByQuery),
	}

	if err := chromedp.Run(ctx, tasks...); err != nil {
		return "", err
	}
	return page, nil
}

type mastodonLookupAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Acct     string `json:"acct"`
	URL      string `json:"url"`
}

type mastodonMediaAttachment struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	RemoteURL string `json:"remote_url"`
}

type mastodonStatus struct {
	ID               string                    `json:"id"`
	CreatedAt        string                    `json:"created_at"`
	Content          string                    `json:"content"`
	URL              string                    `json:"url"`
	MediaAttachments []mastodonMediaAttachment `json:"media_attachments"`
}

type mastodonFeedPayload struct {
	Account  mastodonLookupAccount `json:"account"`
	Statuses []mastodonStatus      `json:"statuses"`
}

func fetchPostsViaBrowserAPI(profileURL string, cfg Config, limit int) ([]Post, error) {
	account := extractUsernameFromEntry(profileURL)
	if account == "" {
		return nil, fmt.Errorf("empty Truth Social account")
	}

	candidates := browserProfileCandidates()
	var lastErr error
	for _, userDataDir := range candidates {
		posts, err := fetchPostsViaBrowserAPIWithProfile(profileURL, cfg, limit, userDataDir)
		if err == nil {
			return posts, nil
		}
		if err != nil {
			lastErr = err
			log.Printf("browser profile %s fetch failed for %s: %v", userDataDir, profileURL, err)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable browser profile with Truth Social auth was found")
	}
	return nil, lastErr
}

func fetchPostsViaBrowserAPIWithProfile(profileURL string, cfg Config, limit int, userDataDir string) ([]Post, error) {
	account := extractUsernameFromEntry(profileURL)
	ctx, cleanup, err := newBrowserContext(userDataDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var authToken string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(profileURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Evaluate(`(() => {
				try {
					const raw = localStorage.getItem('truth:auth');
					if (!raw) return '';
					const auth = JSON.parse(raw);
					const users = auth && auth.users ? auth.users : {};
					const current = auth && auth.me && users[auth.me] ? users[auth.me] : null;
					const first = current || Object.values(users)[0] || null;
					return first && first.access_token ? first.access_token : '';
				} catch (e) {
					return '';
				}
			})()`, &authToken).Do(ctx)
		}),
	); err != nil {
		return nil, err
	}

	tokens := bearerTokenCandidates(cfg, authToken)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no Truth Social access token available")
	}

	var lastErr error
	for _, token := range tokens {
		posts, err := fetchPostsViaBrowserAPIWithToken(ctx, account, token, limit)
		if err == nil {
			return posts, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func fetchPostsViaBrowserAPIWithToken(ctx context.Context, account, token string, limit int) ([]Post, error) {
	var raw string
	js := fmt.Sprintf(`(async () => {
		const acct = %s;
		const headers = { "Accept": "application/json", "Authorization": "Bearer " + %s };
		const lookupRes = await fetch("https://truthsocial.com/api/v1/accounts/lookup?acct=" + encodeURIComponent(acct), {
			credentials: "include",
			headers
		});
		if (!lookupRes.ok) {
			throw new Error("lookup failed: " + lookupRes.status);
		}
		const account = await lookupRes.json();
		const statusesRes = await fetch("https://truthsocial.com/api/v1/accounts/" + account.id + "/statuses?limit=%d", {
			credentials: "include",
			headers
		});
		if (!statusesRes.ok) {
			throw new Error("statuses failed: " + statusesRes.status);
		}
		return JSON.stringify({ account: account, statuses: await statusesRes.json() });
	})()`, strconv.Quote(account), strconv.Quote(token), limit)

	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return chromedp.Evaluate(js, &raw,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			},
		).Do(ctx)
	})); err != nil {
		return nil, err
	}

	var payload mastodonFeedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	if len(payload.Statuses) == 0 {
		return []Post{}, nil
	}

	posts := make([]Post, 0, len(payload.Statuses))
	username := payload.Account.Username
	if username == "" {
		username = payload.Account.Acct
	}
	if username == "" {
		username = account
	}
	for _, status := range payload.Statuses {
		posts = append(posts, Post{
			ID:        status.ID,
			Username:  username,
			Content:   extractContent(status.Content),
			WebURL:    resolveStatusURL(status.URL, username, status.ID),
			VideoURL:  selectVideoURL(status.MediaAttachments),
			Timestamp: normalizePostTimestamp(status.CreatedAt),
			Status:    PostStatusNormal,
		})
	}
	sortPostsByFreshness(posts)
	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
	}
	return posts, nil
}

func newBrowserContext(userDataDir string) (context.Context, func(), error) {
	chromePath, err := findChromeExecPath()
	if err != nil {
		return nil, nil, err
	}

	userDataDir, err = filepath.Abs(userDataDir)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return nil, nil, err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(userDataDir),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("excludeSwitches", "enable-automation"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	ctx, timeoutCancel := context.WithTimeout(ctx, 90*time.Second)

	cleanup := func() {
		timeoutCancel()
		ctxCancel()
		allocCancel()
	}
	return ctx, cleanup, nil
}

func browserProfileCandidates() []string {
	candidates := []string{}
	if override := strings.TrimSpace(os.Getenv("TRUTHSOCIAL_BROWSER_PROFILE_DIR")); override != "" {
		candidates = append(candidates, override)
	}
	candidates = append(candidates,
		".chrome-profile",
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "User Data"),
	)
	seen := map[string]struct{}{}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(candidate)]; ok {
			continue
		}
		seen[strings.ToLower(candidate)] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			result = append(result, candidate)
		}
	}
	return result
}

func waitForRenderablePosts(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	selectorExpr := `document.querySelectorAll("article[data-id], div[data-id], article[data-testid='post'], div[data-testid='post'], div.status[data-id]").length`
	for {
		var count int64
		if err := chromedp.Evaluate(selectorExpr, &count).Do(ctx); err == nil && count > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Truth Social posts to render")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func sortPostsByFreshness(posts []Post) {
	sort.Slice(posts, func(i, j int) bool {
		ti := parsePostTime(posts[i].Timestamp)
		tj := parsePostTime(posts[j].Timestamp)
		if ti.Equal(tj) {
			return posts[i].ID > posts[j].ID
		}
		return ti.After(tj)
	})
}

func resolveStatusURL(rawURL, username, postID string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		return rawURL
	}
	if username == "" || postID == "" {
		return ""
	}
	return fmt.Sprintf("https://truthsocial.com/@%s/posts/%s", username, postID)
}

func selectVideoURL(media []mastodonMediaAttachment) string {
	for _, attachment := range media {
		kind := strings.ToLower(strings.TrimSpace(attachment.Type))
		if kind != "video" && kind != "gifv" {
			continue
		}
		if url := strings.TrimSpace(attachment.URL); url != "" {
			return url
		}
		if url := strings.TrimSpace(attachment.RemoteURL); url != "" {
			return url
		}
	}
	for _, attachment := range media {
		if url := strings.TrimSpace(attachment.URL); url != "" {
			return url
		}
		if url := strings.TrimSpace(attachment.RemoteURL); url != "" {
			return url
		}
	}
	return ""
}

func fetchPageHTMLWithHTTP(profileURL string, cfg Config) (string, error) {
	candidates := bearerTokenCandidates(cfg, "")
	if len(candidates) == 0 {
		candidates = []string{""}
	}

	var lastErr error
	for _, token := range candidates {
		page, err := fetchPageHTMLWithHTTPToken(profileURL, token)
		if err == nil {
			return page, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func fetchPageHTMLWithHTTPToken(profileURL, token string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, profileURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
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

func isCloudflareBlock(page string) bool {
	lower := strings.ToLower(page)
	return strings.Contains(lower, "attention required! | cloudflare") ||
		strings.Contains(lower, "please enable cookies") ||
		strings.Contains(lower, "you are unable to access truthsocial.com") ||
		strings.Contains(lower, "cloudflare ray id")
}

func findChromeExecPath() (string, error) {
	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Chrome/Edge executable found")
}
