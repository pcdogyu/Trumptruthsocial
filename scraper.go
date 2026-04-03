package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	debugf("fetchLatestPostsWithLimit start: profile=%s limit=%d", profileURL, limit)
	var apiErr error
	if posts, err := fetchPostsViaBrowserAPI(profileURL, cfg, limit); err == nil && len(posts) > 0 {
		debugf("fetchLatestPostsWithLimit browser api success: profile=%s posts=%d", profileURL, len(posts))
		return posts, nil
	} else if err != nil {
		apiErr = err
		log.Printf("Truth Social token/API fetch failed for %s: %v", profileURL, err)
	}

	page, err := fetchPageHTMLWithHTTP(profileURL, cfg)
	if err != nil && browserExecutableAvailable() {
		page, err = fetchPageHTMLWithBrowser(profileURL)
	}
	if err != nil {
		log.Printf("Truth Social page fetch failed for %s: %v", profileURL, err)
		return nil, err
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
	debugf("fetchLatestPostsWithLimit html parsed: profile=%s posts=%d", profileURL, len(posts))
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
	userDataDir, err := filepath.Abs(".chrome-profile")
	if err != nil {
		return "", err
	}
	var page string
	err = runBrowserTaskWithProfileFallback(userDataDir, func(ctx context.Context) error {
		tasks := []chromedp.Action{
			chromedp.Navigate(profileURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				return waitForRenderablePosts(ctx, 60*time.Second)
			}),
			chromedp.OuterHTML("html", &page, chromedp.ByQuery),
		}
		if err := chromedp.Run(ctx, tasks...); err != nil {
			return err
		}

		bestPage := page
		bestCount := len(mergePostOpenings(page))
		if err := chromedp.Run(ctx, chromedp.MouseClickXY(200, 200)); err != nil {
			log.Printf("browser focus click failed for %s: %v", profileURL, err)
		}

		for i := 0; i < 8; i++ {
			if err := chromedp.Run(ctx,
				chromedp.KeyEvent(" "),
				chromedp.Sleep(2*time.Second),
				chromedp.ActionFunc(func(ctx context.Context) error {
					return chromedp.OuterHTML("html", &page, chromedp.ByQuery).Do(ctx)
				}),
			); err != nil {
				log.Printf("browser space scroll failed for %s on attempt %d: %v", profileURL, i+1, err)
				break
			}
			if count := len(mergePostOpenings(page)); count > bestCount {
				bestCount = count
				bestPage = page
			}
		}
		page = bestPage
		return nil
	})
	if err != nil {
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
	debugf("fetchPostsViaBrowserAPI start: account=%s limit=%d", account, limit)

	if posts, err := fetchPostsViaBrowserAPIWithConfigTokens(account, cfg, limit); err == nil && len(posts) > 0 {
		debugf("fetchPostsViaBrowserAPI config token success: account=%s posts=%d", account, len(posts))
		return posts, nil
	} else if err != nil {
		log.Printf("Truth Social token/API fetch failed for %s: %v", profileURL, err)
	}

	if !browserExecutableAvailable() {
		return fetchPostsViaBrowserAPIWithConfigTokens(account, cfg, limit)
	}

	candidates := browserProfileCandidates()
	var lastErr error
	for _, userDataDir := range candidates {
		debugf("fetchPostsViaBrowserAPI trying browser profile: account=%s profile=%s", account, userDataDir)
		posts, err := fetchPostsViaBrowserAPIWithProfile(profileURL, cfg, limit, userDataDir)
		if err == nil {
			debugf("fetchPostsViaBrowserAPI browser profile success: account=%s profile=%s posts=%d", account, userDataDir, len(posts))
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

func fetchPostsViaBrowserAPIWithConfigTokens(account string, cfg Config, limit int) ([]Post, error) {
	tokens := bearerTokenCandidates(cfg, "")
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no Truth Social access token available")
	}

	var lastErr error
	for _, token := range tokens {
		posts, err := fetchPostsViaHTTP(account, token, limit)
		if err == nil {
			return posts, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func fetchHistoricalPostsViaBrowserAPI(profileURL string, cfg Config, cutoff time.Time) ([]Post, error) {
	account := extractUsernameFromEntry(profileURL)
	if account == "" {
		return nil, fmt.Errorf("empty Truth Social account")
	}
	debugf("fetchHistoricalPostsViaBrowserAPI start: account=%s cutoff=%s", account, cutoff.Format(time.RFC3339))

	sawSuccess := false
	if posts, err := fetchHistoricalPostsViaBrowserAPIWithConfigTokens(account, cfg, cutoff); err == nil && len(posts) > 0 {
		debugf("fetchHistoricalPostsViaBrowserAPI config token success: account=%s posts=%d", account, len(posts))
		return posts, nil
	} else if err != nil {
		log.Printf("browser token historical fetch failed for %s: %v", profileURL, err)
	} else if err == nil {
		sawSuccess = true
	}

	if !browserExecutableAvailable() {
		return fetchHistoricalPostsViaBrowserAPIWithConfigTokens(account, cfg, cutoff)
	}

	candidates := browserProfileCandidates()
	var lastErr error
	for _, userDataDir := range candidates {
		debugf("fetchHistoricalPostsViaBrowserAPI trying browser profile: account=%s profile=%s", account, userDataDir)
		posts, err := fetchHistoricalPostsViaBrowserAPIWithProfile(profileURL, cfg, cutoff, userDataDir)
		if err == nil {
			if len(posts) > 0 {
				debugf("fetchHistoricalPostsViaBrowserAPI browser profile success: account=%s profile=%s posts=%d", account, userDataDir, len(posts))
				return posts, nil
			}
			sawSuccess = true
			continue
		}
		if err != nil {
			lastErr = err
			log.Printf("browser profile %s historical fetch failed for %s: %v", userDataDir, profileURL, err)
		}
	}
	if sawSuccess {
		return []Post{}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable browser profile with Truth Social auth was found")
	}
	return nil, lastErr
}

func browserExecutableAvailable() bool {
	_, err := findChromeExecPath()
	return err == nil
}

func fetchHistoricalPosts(profileURL string, cfg Config, cutoff time.Time) ([]Post, error) {
	return fetchHistoricalPostsViaBrowserAPI(profileURL, cfg, cutoff)
}

func fetchHistoricalPostsViaBrowserAPIWithConfigTokens(account string, cfg Config, cutoff time.Time) ([]Post, error) {
	tokens := bearerTokenCandidates(cfg, "")
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no Truth Social access token available")
	}

	var lastErr error
	for _, token := range tokens {
		posts, err := fetchHistoricalPostsViaHTTP(account, token, cutoff)
		if err == nil {
			return posts, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func fetchPostsViaBrowserAPIWithProfile(profileURL string, cfg Config, limit int, userDataDir string) ([]Post, error) {
	account := extractUsernameFromEntry(profileURL)
	var authToken string
	var posts []Post
	err := runBrowserTaskWithProfileFallback(userDataDir, func(ctx context.Context) error {
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
			return err
		}

		tokens := bearerTokenCandidates(cfg, authToken)
		if len(tokens) == 0 {
			return fmt.Errorf("no Truth Social access token available")
		}

		var lastErr error
		for _, token := range tokens {
			p, err := fetchPostsViaHTTP(account, token, limit)
			if err == nil {
				posts = p
				return nil
			}
			lastErr = err
		}
		return lastErr
	})
	if err != nil {
		return nil, err
	}
	return posts, nil
}

func fetchHistoricalPostsViaBrowserAPIWithProfile(profileURL string, cfg Config, cutoff time.Time, userDataDir string) ([]Post, error) {
	account := extractUsernameFromEntry(profileURL)
	var authToken string
	var posts []Post
	err := runBrowserTaskWithProfileFallback(userDataDir, func(ctx context.Context) error {
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
			return err
		}

		tokens := bearerTokenCandidates(cfg, authToken)
		if len(tokens) == 0 {
			return fmt.Errorf("no Truth Social access token available")
		}

		var lastErr error
		for _, token := range tokens {
			p, err := fetchHistoricalPostsViaHTTP(account, token, cutoff)
			if err == nil {
				posts = p
				return nil
			}
			lastErr = err
		}
		return lastErr
	})
	if err != nil {
		return nil, err
	}
	return posts, nil
}

func fetchPostsViaBrowserAPIWithToken(ctx context.Context, account, token string, limit int) ([]Post, error) {
	_ = ctx
	return fetchPostsViaHTTP(account, token, limit)
}

func fetchHistoricalPostsViaBrowserAPIWithToken(ctx context.Context, account, token string, cutoff time.Time) ([]Post, error) {
	_ = ctx
	return fetchHistoricalPostsViaHTTP(account, token, cutoff)
}

func fetchLookupAccountViaHTTP(account, token string) (mastodonLookupAccount, error) {
	var lookupAccount mastodonLookupAccount
	if err := doTruthSocialJSONRequest(http.MethodGet, "https://truthsocial.com/api/v1/accounts/lookup?acct="+url.QueryEscape(account), token, nil, &lookupAccount); err != nil {
		return mastodonLookupAccount{}, err
	}
	if lookupAccount.ID == "" {
		return mastodonLookupAccount{}, fmt.Errorf("lookup returned empty account id for %s", account)
	}
	return lookupAccount, nil
}

func fetchStatusesPageViaHTTP(accountID, token string, limit int, maxID string) ([]mastodonStatus, error) {
	urlValues := url.Values{}
	if limit > 0 {
		urlValues.Set("limit", strconv.Itoa(limit))
	}
	if maxID != "" {
		urlValues.Set("max_id", maxID)
	}

	statusesURL := "https://truthsocial.com/api/v1/accounts/" + url.QueryEscape(accountID) + "/statuses"
	if encoded := urlValues.Encode(); encoded != "" {
		statusesURL += "?" + encoded
	}

	var statuses []mastodonStatus
	if err := doTruthSocialJSONRequest(http.MethodGet, statusesURL, token, nil, &statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

func fetchPostsViaHTTP(account, token string, limit int) ([]Post, error) {
	lookupAccount, err := fetchLookupAccountViaHTTP(account, token)
	if err != nil {
		return nil, err
	}

	statuses, err := fetchStatusesPageViaHTTP(lookupAccount.ID, token, limit, "")
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return []Post{}, nil
	}

	posts := statusesToPosts(account, lookupAccount, statuses)
	if limit > 0 && len(posts) > limit {
		posts = posts[:limit]
	}
	return posts, nil
}

func fetchHistoricalPostsViaHTTP(account, token string, cutoff time.Time) ([]Post, error) {
	lookupAccount, err := fetchLookupAccountViaHTTP(account, token)
	if err != nil {
		return nil, err
	}

	const pageLimit = 40
	const maxPages = 20

	collected := map[string]Post{}
	maxID := ""
	for page := 0; page < maxPages; page++ {
		statuses, err := fetchStatusesPageViaHTTP(lookupAccount.ID, token, pageLimit, maxID)
		if err != nil {
			return nil, err
		}
		if len(statuses) == 0 {
			break
		}
		log.Printf("historical page fetched for %s: page=%d statuses=%d max_id=%s", account, page+1, len(statuses), maxID)

		reachedCutoff := false
		for _, status := range statuses {
			post := statusToPost(account, lookupAccount, status)
			if post.ID == "" {
				continue
			}
			if !cutoff.IsZero() {
				if t := parsePostTime(post.Timestamp); !t.IsZero() && t.Before(cutoff) {
					reachedCutoff = true
					break
				}
			}
			collected[post.ID] = post
		}
		if reachedCutoff {
			break
		}

		lastID := strings.TrimSpace(statuses[len(statuses)-1].ID)
		if lastID == "" || lastID == maxID || len(statuses) < pageLimit {
			break
		}
		maxID = lastID
	}

	posts := make([]Post, 0, len(collected))
	for _, post := range collected {
		posts = append(posts, post)
	}
	sortPostsByFreshness(posts)
	return posts, nil
}

func doTruthSocialJSONRequest(method, rawURL, token string, body io.Reader, target any) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("response parse failed: %w", err)
	}
	return nil
}

func statusesToPosts(account string, lookupAccount mastodonLookupAccount, statuses []mastodonStatus) []Post {
	posts := make([]Post, 0, len(statuses))
	for _, status := range statuses {
		post := statusToPost(account, lookupAccount, status)
		if post.ID != "" {
			posts = append(posts, post)
		}
	}
	sortPostsByFreshness(posts)
	return posts
}

func statusToPost(account string, lookupAccount mastodonLookupAccount, status mastodonStatus) Post {
	username := lookupAccount.Username
	if username == "" {
		username = lookupAccount.Acct
	}
	if username == "" {
		username = account
	}
	return Post{
		ID:        status.ID,
		Username:  username,
		Content:   extractContent(status.Content),
		WebURL:    resolveStatusURL(status.URL, username, status.ID),
		ImageURL:  selectImageURL(status.MediaAttachments),
		VideoURL:  selectVideoURL(status.MediaAttachments),
		Timestamp: normalizePostTimestamp(status.CreatedAt),
		Status:    PostStatusNormal,
	}
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

func runEphemeralBrowserTask(task func(context.Context) error) error {
	chromePath, err := findChromeExecPath()
	if err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("excludeSwitches", "enable-automation"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, 90*time.Second)
	defer timeoutCancel()

	return task(ctx)
}

func runBrowserTaskWithProfileFallback(userDataDir string, task func(context.Context) error) error {
	ctx, cleanup, err := newBrowserContext(userDataDir)
	if err != nil {
		return err
	}
	defer cleanup()

	err = task(ctx)
	if err == nil || !isChromeStartFailure(err) {
		return err
	}

	cloneDir, cloneCleanup, cloneErr := cloneBrowserProfile(userDataDir)
	if cloneErr != nil {
		return err
	}
	defer cloneCleanup()

	ctx2, cleanup2, err := newBrowserContext(cloneDir)
	if err != nil {
		return err
	}
	defer cleanup2()

	return task(ctx2)
}

func isChromeStartFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "chrome failed to start") ||
		strings.Contains(msg, "browser has disconnected")
}

func cloneBrowserProfile(src string) (string, func(), error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return "", nil, err
	}
	dst, err := os.MkdirTemp("", "truthsocial-profile-clone-*")
	if err != nil {
		return "", nil, err
	}

	copyList := []string{
		"Local State",
		filepath.Join("Default", "Local Storage"),
		filepath.Join("Default", "Network"),
		filepath.Join("Default", "Preferences"),
		filepath.Join("Default", "Secure Preferences"),
		filepath.Join("Default", "Session Storage"),
		filepath.Join("Default", "WebStorage"),
		filepath.Join("Default", "IndexedDB"),
	}

	for _, rel := range copyList {
		srcPath := filepath.Join(srcAbs, rel)
		if _, err := os.Stat(srcPath); err != nil {
			continue
		}
		dstPath := filepath.Join(dst, rel)
		if err := copyProfilePath(srcPath, dstPath); err != nil {
			_ = os.RemoveAll(dst)
			return "", nil, err
		}
	}

	cleanup := func() {
		_ = os.RemoveAll(dst)
	}
	return dst, cleanup, nil
}

func copyProfilePath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if shouldSkipProfileEntry(entry.Name()) {
				continue
			}
			if err := copyProfilePath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if shouldSkipProfileEntry(filepath.Base(src)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func shouldSkipProfileEntry(name string) bool {
	switch strings.ToLower(name) {
	case "lockfile", "devtoolsactiveport", "singletoncookie", "singletonlock", "singlesession":
		return true
	default:
		return false
	}
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
		if mediaAttachmentKind(attachment) != "video" {
			continue
		}
		if url := mediaAttachmentURL(attachment); url != "" {
			return url
		}
	}
	return ""
}

func selectImageURL(media []mastodonMediaAttachment) string {
	for _, attachment := range media {
		if mediaAttachmentKind(attachment) != "image" {
			continue
		}
		if url := mediaAttachmentURL(attachment); url != "" {
			return url
		}
	}
	return ""
}

func mediaAttachmentURL(attachment mastodonMediaAttachment) string {
	if url := strings.TrimSpace(attachment.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(attachment.RemoteURL); url != "" {
		return url
	}
	return ""
}

func mediaAttachmentKind(attachment mastodonMediaAttachment) string {
	kind := strings.ToLower(strings.TrimSpace(attachment.Type))
	switch kind {
	case "video", "gifv":
		return "video"
	case "image", "photo", "jpeg", "png", "jpg", "gif":
		return "image"
	}

	rawURL := mediaAttachmentURL(attachment)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".mp4", ".mov", ".m4v", ".webm", ".ogg":
		return "video"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return "image"
	default:
		return ""
	}
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
	if override := strings.TrimSpace(os.Getenv("TRUTHSOCIAL_CHROME_PATH")); override != "" {
		if resolved, err := resolveChromeCandidate(override); err == nil {
			return resolved, nil
		}
	}

	candidates := []string{
		"google-chrome",
		"google-chrome-stable",
		"google-chrome-beta",
		"google-chrome-unstable",
		"chrome",
		"chromium",
		"chromium-browser",
		"chromium-chromedriver",
		"brave-browser",
		"brave",
		"vivaldi",
		"microsoft-edge",
		"msedge",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome-beta",
		"/usr/bin/google-chrome-unstable",
		"/usr/bin/chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/brave-browser",
		"/usr/bin/brave",
		"/usr/bin/vivaldi",
		"/usr/bin/microsoft-edge",
		"/usr/bin/msedge",
		"/snap/chromium/current/usr/lib/chromium-browser/chrome",
		"/snap/chromium/current/usr/lib/chromium/chromium",
		"/snap/bin/chromium",
		"/opt/google/chrome/google-chrome",
		"/opt/google/chrome/chrome",
		"/usr/local/bin/google-chrome",
		"/usr/local/bin/chromium",
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	}
	tried := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		resolved, err := resolveChromeCandidate(candidate)
		if err == nil {
			return resolved, nil
		}
		tried = append(tried, candidate)
	}
	return "", fmt.Errorf("no Chrome/Edge executable found; tried %s; set TRUTHSOCIAL_CHROME_PATH to the full browser path", strings.Join(tried, ", "))
}

func resolveChromeCandidate(candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("empty browser path")
	}
	if strings.Contains(candidate, string(os.PathSeparator)) || strings.Contains(candidate, `\`) {
		if info, err := os.Stat(candidate); err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("browser path is a directory: %s", candidate)
			}
			if strings.EqualFold(filepath.Clean(candidate), filepath.Clean("/snap/bin/chromium")) {
				return "", fmt.Errorf("skip snap launcher wrapper: %s", candidate)
			}
			return candidate, nil
		}
		return "", fmt.Errorf("browser path not found: %s", candidate)
	}
	if resolved, err := exec.LookPath(candidate); err == nil {
		if strings.EqualFold(filepath.Clean(resolved), filepath.Clean("/usr/bin/snap")) {
			return "", fmt.Errorf("skip snap launcher wrapper: %s", candidate)
		}
		if strings.EqualFold(filepath.Clean(resolved), filepath.Clean("/snap/bin/chromium")) {
			return "", fmt.Errorf("skip snap launcher wrapper: %s", candidate)
		}
		return resolved, nil
	}
	return "", fmt.Errorf("browser command not found: %s", candidate)
}
