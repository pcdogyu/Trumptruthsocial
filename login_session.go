package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	loginSessionLoginURL       = "https://truthsocial.com/login"
	loginSessionWidth          = 1280
	loginSessionHeight         = 900
	loginSessionPollInterval   = 2 * time.Second
	loginSessionTimeout        = 15 * time.Minute
	loginSessionCleanupDelay   = 2 * time.Minute
	loginSessionDisplayStart   = 80
	loginSessionDisplayEnd     = 199
	loginSessionChromiumDelay  = 1200 * time.Millisecond
	loginSessionVNCListenHost  = "127.0.0.1"
	loginSessionBrowserAddress = "127.0.0.1"
)

type CapturedCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"http_only"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"same_site,omitempty"`
	Priority string  `json:"priority,omitempty"`
}

type LoginSessionState string

const (
	LoginSessionStarting LoginSessionState = "starting"
	LoginSessionRunning  LoginSessionState = "running"
	LoginSessionSuccess  LoginSessionState = "success"
	LoginSessionError    LoginSessionState = "error"
	LoginSessionClosed   LoginSessionState = "closed"
)

type LoginSession struct {
	ID         string
	Username   string
	ProfileDir string
	Display    int
	VNCPort    int
	DebugPort  int
	Chromium   string
	LoginURL   string
	StartedAt  time.Time

	mu        sync.RWMutex
	state     LoginSessionState
	message   string
	token     string
	cookies   []CapturedCookie
	done      chan struct{}
	closeOnce sync.Once
	xvfbCmd   *exec.Cmd
	x11vncCmd *exec.Cmd
	chromeCmd *exec.Cmd
	manager   *LoginSessionManager
}

type LoginSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*LoginSession
}

func newLoginSessionManager() *LoginSessionManager {
	return &LoginSessionManager{
		sessions: make(map[string]*LoginSession),
	}
}

func (m *LoginSessionManager) Start(username string) (*LoginSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		if s != nil {
			switch s.State() {
			case LoginSessionStarting, LoginSessionRunning:
				return s, nil
			}
		}
	}

	sess, err := newLoginSession(username)
	if err != nil {
		return nil, err
	}
	sess.manager = m
	m.sessions[sess.ID] = sess
	if err := sess.start(); err != nil {
		delete(m.sessions, sess.ID)
		return nil, err
	}
	go sess.monitor()
	return sess, nil
}

func (m *LoginSessionManager) Get(id string) (*LoginSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *LoginSessionManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

func newLoginSession(username string) (*LoginSession, error) {
	display, err := chooseDisplayNumber()
	if err != nil {
		return nil, err
	}
	vncPort, err := freeTCPPort()
	if err != nil {
		return nil, err
	}
	debugPort, err := freeTCPPort()
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp("", "truthsocial-login-session-*")
	if err != nil {
		return nil, err
	}

	chromiumPath, err := findChromeExecPath()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}

	return &LoginSession{
		ID:         randID("login"),
		Username:   username,
		ProfileDir: profileDir,
		Display:    display,
		VNCPort:    vncPort,
		DebugPort:  debugPort,
		Chromium:   chromiumPath,
		LoginURL:   loginSessionLoginURL,
		StartedAt:  time.Now().UTC(),
		state:      LoginSessionStarting,
		message:    "正在启动远程登录窗口...",
		done:       make(chan struct{}),
	}, nil
}

func (s *LoginSession) start() error {
	if err := ensureX11VNCInstalled(); err != nil {
		s.setError(fmt.Errorf("启动远程登录窗口失败: %w", err))
		s.cleanup()
		return err
	}

	if err := s.startXvfb(); err != nil {
		s.setError(fmt.Errorf("启动虚拟显示失败: %w", err))
		s.cleanup()
		return err
	}

	time.Sleep(loginSessionChromiumDelay)

	if err := s.startX11VNC(); err != nil {
		s.setError(fmt.Errorf("启动 VNC 服务失败: %w", err))
		s.cleanup()
		return err
	}

	if err := s.startChromium(); err != nil {
		s.setError(fmt.Errorf("启动浏览器失败: %w", err))
		s.cleanup()
		return err
	}

	s.setRunning("远程登录窗口已打开，请在弹出的窗口中完成 Truth Social 登录。")
	return nil
}

func (s *LoginSession) startXvfb() error {
	displayArg := ":" + strconv.Itoa(s.Display)
	cmd := exec.Command("Xvfb", displayArg, "-screen", "0", fmt.Sprintf("%dx%dx24", loginSessionWidth, loginSessionHeight), "-ac", "-nolisten", "tcp")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.xvfbCmd = cmd
	go s.waitOnProcess("Xvfb", cmd)
	return nil
}

func (s *LoginSession) startX11VNC() error {
	displayArg := ":" + strconv.Itoa(s.Display)
	cmd := exec.Command("x11vnc",
		"-display", displayArg,
		"-rfbport", strconv.Itoa(s.VNCPort),
		"-localhost",
		"-forever",
		"-shared",
		"-nopw",
		"-noxdamage",
		"-noxfixes",
		"-noxrecord",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DISPLAY="+displayArg)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.x11vncCmd = cmd
	go s.waitOnProcess("x11vnc", cmd)
	return nil
}

func (s *LoginSession) startChromium() error {
	displayArg := ":" + strconv.Itoa(s.Display)
	runtimeDir := filepath.Join(os.TempDir(), "truthsocial-runtime-"+s.ID)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}

	cmd := exec.Command(s.Chromium,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-blink-features=AutomationControlled",
		"--exclude-switches=enable-automation",
		"--window-size="+strconv.Itoa(loginSessionWidth)+","+strconv.Itoa(loginSessionHeight),
		"--remote-debugging-address="+loginSessionBrowserAddress,
		"--remote-debugging-port="+strconv.Itoa(s.DebugPort),
		"--user-data-dir="+s.ProfileDir,
		"--no-sandbox",
		s.LoginURL,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"DISPLAY="+displayArg,
		"XDG_RUNTIME_DIR="+runtimeDir,
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.chromeCmd = cmd
	go s.waitOnProcess("chromium", cmd)
	return nil
}

func (s *LoginSession) waitOnProcess(name string, cmd *exec.Cmd) {
	if err := cmd.Wait(); err != nil {
		debugf("login session process exited: session=%s process=%s err=%v", s.ID, name, err)
	}
}

func (s *LoginSession) monitor() {
	defer func() {
		s.markClosed("登录会话已结束。")
	}()

	deadline := time.Now().Add(loginSessionTimeout)
	debugURL := fmt.Sprintf("http://%s:%d", loginSessionBrowserAddress, s.DebugPort)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		if time.Now().After(deadline) {
			s.setError(errors.New("登录会话超时，请重新打开登录窗口。"))
			return
		}

		token, cookies, err := readTokenAndCookiesFromRemoteBrowser(debugURL, s.LoginURL)
		if err != nil {
			debugf("login session token poll failed: session=%s err=%v", s.ID, err)
			time.Sleep(loginSessionPollInterval)
			continue
		}

		token = strings.TrimSpace(token)
		if token == "" {
			time.Sleep(loginSessionPollInterval)
			continue
		}

		if err := persistBearerToken(token); err != nil {
			s.setError(fmt.Errorf("登录成功但写回 Bearer Token 失败: %w", err))
			s.setToken(token, cookies)
			return
		}

		if err := persistLoginSessionData(s.ID, s.Username, token, cookies); err != nil {
			log.Printf("login session data persist failed: session=%s err=%v", s.ID, err)
		}

		s.setSuccess(token, cookies)
		go func(id string) {
			time.Sleep(loginSessionCleanupDelay)
			s.cleanup()
			if s.manager != nil {
				s.manager.remove(id)
			}
		}(s.ID)
		return
	}
}

func (s *LoginSession) stop() {
	s.closeOnce.Do(func() {
		close(s.done)
	})
}

func (s *LoginSession) cleanup() {
	s.stop()
	if s.chromeCmd != nil && s.chromeCmd.Process != nil {
		_ = s.chromeCmd.Process.Kill()
	}
	if s.x11vncCmd != nil && s.x11vncCmd.Process != nil {
		_ = s.x11vncCmd.Process.Kill()
	}
	if s.xvfbCmd != nil && s.xvfbCmd.Process != nil {
		_ = s.xvfbCmd.Process.Kill()
	}
	_ = os.RemoveAll(s.ProfileDir)
	_ = os.RemoveAll(filepath.Join(os.TempDir(), "truthsocial-runtime-"+s.ID))
	s.markClosed("登录会话已关闭。")
}

func (s *LoginSession) setRunning(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionRunning
	s.message = message
}

func (s *LoginSession) setSuccess(token string, cookies []CapturedCookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionSuccess
	s.message = "登录完成，Bearer Token 已写回后端。"
	s.token = token
	s.cookies = cookies
}

func (s *LoginSession) setToken(token string, cookies []CapturedCookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	s.cookies = cookies
}

func (s *LoginSession) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionError
	s.message = err.Error()
}

func (s *LoginSession) markClosed(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != LoginSessionSuccess && s.state != LoginSessionError {
		s.state = LoginSessionClosed
	}
	if message != "" {
		s.message = message
	}
}

func (s *LoginSession) State() LoginSessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *LoginSession) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token := ""
	if s.token != "" {
		token = maskToken(s.token)
	}
	return map[string]any{
		"id":            s.ID,
		"username":      s.Username,
		"state":         string(s.state),
		"message":       s.message,
		"started_at":    s.StartedAt,
		"login_url":     s.LoginURL,
		"vnc_port":      s.VNCPort,
		"debug_port":    s.DebugPort,
		"token":         token,
		"cookie_count":  len(s.cookies),
		"cookie_names":  cookieNames(s.cookies),
		"profile_dir":   s.ProfileDir,
		"chromium_path": s.Chromium,
	}
}

func (s *LoginSession) attachAndReadCookieData() (string, []CapturedCookie, error) {
	debugURL := fmt.Sprintf("http://%s:%d", loginSessionBrowserAddress, s.DebugPort)
	return readTokenAndCookiesFromRemoteBrowser(debugURL, s.LoginURL)
}

func persistLoginSessionData(sessionID, username, token string, cookies []CapturedCookie) error {
	payload := map[string]any{
		"session_id":   sessionID,
		"username":     username,
		"bearer_token": token,
		"cookies":      cookies,
		"captured_at":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("truthsocial_login_session.json", data, 0o600)
}

func readTokenAndCookiesFromRemoteBrowser(debugURL, loginURL string) (string, []CapturedCookie, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), debugURL)
	defer allocCancel()

	targets, err := chromedp.Targets(allocCtx)
	if err != nil {
		return "", nil, err
	}

	var pageTarget *target.Info
	for _, t := range targets {
		if t.Type == "page" && strings.HasPrefix(t.URL, loginURL) {
			pageTarget = t
			break
		}
	}
	if pageTarget == nil {
		for _, t := range targets {
			if t.Type == "page" {
				pageTarget = t
				break
			}
		}
	}
	if pageTarget == nil {
		return "", nil, fmt.Errorf("no browser page target found")
	}

	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(pageTarget.TargetID))
	defer cancel()

	var token string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(function() {
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
	})()`, &token)); err != nil {
		return "", nil, err
	}

	cookies, err := network.GetCookies().WithUrls([]string{loginURL, "https://truthsocial.com/"}).Do(ctx)
	if err != nil {
		return token, nil, err
	}

	captured := make([]CapturedCookie, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		captured = append(captured, CapturedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
			SameSite: string(c.SameSite),
			Priority: string(c.Priority),
		})
	}

	return strings.TrimSpace(token), captured, nil
}

func cookieNames(cookies []CapturedCookie) []string {
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if strings.TrimSpace(c.Name) != "" {
			names = append(names, c.Name)
		}
	}
	return names
}

func chooseDisplayNumber() (int, error) {
	for display := loginSessionDisplayStart; display <= loginSessionDisplayEnd; display++ {
		socket := filepath.Join("/tmp/.X11-unix", fmt.Sprintf("X%d", display))
		if _, err := os.Stat(socket); err == nil {
			continue
		}
		return display, nil
	}
	return 0, fmt.Errorf("no free X display found")
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", loginSessionVNCListenHost+":0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type")
	}
	return addr.Port, nil
}

func ensureX11VNCInstalled() error {
	if _, err := exec.LookPath("x11vnc"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("x11vnc not found and apt-get is unavailable")
	}

	log.Println("x11vnc not found; installing it with apt-get")
	update := exec.Command("apt-get", "update")
	update.Stdout = os.Stdout
	update.Stderr = os.Stderr
	if err := update.Run(); err != nil {
		return err
	}

	install := exec.Command("apt-get", "install", "-y", "x11vnc")
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return err
	}
	return nil
}

func randID(prefix string) string {
	n := rand.Int63()
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}

func (s *LoginSession) VNCWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Printf("login session websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	backend, err := net.Dial("tcp", fmt.Sprintf("%s:%d", loginSessionVNCListenHost, s.VNCPort))
	if err != nil {
		log.Printf("login session vnc dial failed: %v", err)
		return
	}
	defer backend.Close()

	errCh := make(chan error, 2)
	go func() {
		for {
			payload, err := wsutil.ReadClientBinary(conn)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
			if len(payload) == 0 {
				continue
			}
			if _, err := backend.Write(payload); err != nil {
				errCh <- err
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := backend.Read(buf)
			if n > 0 {
				if err := wsutil.WriteServerBinary(conn, buf[:n]); err != nil {
					errCh <- err
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
		}
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("login session websocket proxy ended: %v", err)
		}
	case <-time.After(2 * time.Hour):
	}
}
