package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	defaultTokenLoginURL           = "https://truthsocial.com/login"
	defaultTokenTimeoutSeconds     = 180
	defaultTokenPollIntervalSecond = 1
	defaultTokenProfileDir         = ".chrome-token-profile"
)

func runTokenGrabber(args []string) int {
	fs := flag.NewFlagSet("get-token", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	loginURL := fs.String("login-url", defaultTokenLoginURL, "Truth Social 登录地址")
	timeout := fs.Duration("timeout", defaultTokenTimeoutSeconds*time.Second, "最长等待登录的时间")
	pollInterval := fs.Duration("poll-interval", defaultTokenPollIntervalSecond*time.Second, "轮询 localStorage 的间隔")
	profileDir := fs.String("profile-dir", defaultTokenProfileDir, "Chrome 用户数据目录")
	printToken := fs.Bool("print-token", false, "同时在控制台打印完整 Token")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	token, err := fetchBearerTokenWithBrowser(*loginURL, *profileDir, *timeout, *pollInterval)
	if err != nil {
		log.Printf("获取 Bearer Token 失败: %v", err)
		return 1
	}

	if err := persistBearerToken(token); err != nil {
		log.Printf("写回 config.yaml 失败: %v", err)
		return 1
	}

	log.Printf("已获取 Bearer Token: %s", maskToken(token))
	if *printToken {
		fmt.Printf("\nBearer Token: %s\n\n", token)
	}
	return 0
}

func fetchBearerTokenWithBrowser(loginURL, profileDir string, timeout, pollInterval time.Duration) (string, error) {
	ctx, cancel, err := newTokenBrowserContext(profileDir, timeout)
	if err != nil {
		return "", err
	}
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(loginURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return "", err
	}

	log.Println(strings.Repeat("=", 60))
	log.Println("浏览器窗口已打开。请在该窗口中登录 Truth Social。")
	log.Println("登录后程序会自动检测 localStorage 并写回 Bearer Token。")
	log.Println("如果你已经登录过，程序可能会在几秒内直接完成。")
	log.Println(strings.Repeat("=", 60))

	return waitForBearerToken(ctx, timeout, pollInterval)
}

func waitForBearerToken(ctx context.Context, timeout, pollInterval time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		token, err := readBearerTokenFromPage(ctx)
		if err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return "", fmt.Errorf("在 %s 内未检测到 Token", timeout)
}

func readBearerTokenFromPage(ctx context.Context) (string, error) {
	var token string
	js := `(function() {
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
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &token)); err != nil {
		return "", err
	}
	return strings.TrimSpace(token), nil
}

func persistBearerToken(token string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	rotateBearerTokens(&cfg, token)
	return SaveConfig(cfg)
}

func newTokenBrowserContext(userDataDir string, timeout time.Duration) (context.Context, func(), error) {
	chromePath, err := findChromeExecPath()
	if err != nil {
		return nil, nil, err
	}

	userDataDir, err = filepath.Abs(userDataDir)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, nil, err
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(userDataDir),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("excludeSwitches", "enable-automation"),
		chromedp.WindowSize(1280, 900),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	}
	if shouldUseHeadlessTokenBrowser() {
		opts = append(opts, chromedp.Headless)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	ctx, timeoutCancel := context.WithTimeout(ctx, timeout)

	cleanup := func() {
		timeoutCancel()
		ctxCancel()
		allocCancel()
	}
	return ctx, cleanup, nil
}

func shouldUseHeadlessTokenBrowser() bool {
	if v := strings.TrimSpace(os.Getenv("TRUTHSOCIAL_TOKEN_HEADLESS")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return true
	}
	return false
}

func maskToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-4:]
}
