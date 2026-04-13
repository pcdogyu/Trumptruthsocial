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
	"net/url"
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
	loginSessionDesktopURL     = "https://truthsocial.com/login"
	loginSessionWidth          = 1280
	loginSessionHeight         = 900
	loginSessionPollInterval   = 2 * time.Second
	loginSessionProfilePoll    = 12 * time.Second
	loginSessionDebugWarmup    = 60 * time.Second
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
	ID           string
	Username     string
	Password     string
	SessionKind  string
	CaptureToken bool
	ProfileDir   string
	ExtensionDir string
	Display      int
	VNCPort      int
	DebugPort    int
	Chromium     string
	LoginURL     string
	StartedAt    time.Time

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

func (m *LoginSessionManager) Start(username, password string) (*LoginSession, error) {
	m.mu.Lock()
	var staleSessions []*LoginSession
	for id, s := range m.sessions {
		if s == nil {
			delete(m.sessions, id)
			continue
		}
		switch s.State() {
		case LoginSessionStarting, LoginSessionRunning:
			log.Printf("replacing active login session: id=%s state=%s", s.ID, s.State())
			staleSessions = append(staleSessions, s)
			delete(m.sessions, id)
		case LoginSessionClosed, LoginSessionError, LoginSessionSuccess:
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()

	for _, stale := range staleSessions {
		go stale.cleanup()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := newLoginSession(username, password)
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

func (m *LoginSessionManager) StartDesktop() (*LoginSession, error) {
	m.mu.Lock()
	var staleSessions []*LoginSession
	for id, s := range m.sessions {
		if s == nil {
			delete(m.sessions, id)
			continue
		}
		switch s.State() {
		case LoginSessionStarting, LoginSessionRunning:
			log.Printf("replacing active desktop/login session: id=%s state=%s", s.ID, s.State())
			staleSessions = append(staleSessions, s)
			delete(m.sessions, id)
		case LoginSessionClosed, LoginSessionError, LoginSessionSuccess:
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()

	for _, stale := range staleSessions {
		go stale.cleanup()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, err := newDesktopSession()
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

func newLoginSession(username, password string) (*LoginSession, error) {
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
	profileDir, err = filepath.Abs(profileDir)
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}

	baseDir, err := os.Getwd()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}
	extensionRoot := filepath.Join(baseDir, ".truthsocial-login-extension")
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}
	extensionDir, err := os.MkdirTemp(extensionRoot, "session-*")
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}
	extensionDir, err = filepath.Abs(extensionDir)
	if err != nil {
		_ = os.RemoveAll(profileDir)
		_ = os.RemoveAll(extensionDir)
		return nil, err
	}

	chromiumPath, err := findChromeExecPath()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		_ = os.RemoveAll(extensionDir)
		return nil, err
	}

	sess := &LoginSession{
		ID:           randID("login"),
		Username:     username,
		Password:     password,
		SessionKind:  "login",
		CaptureToken: true,
		ProfileDir:   profileDir,
		ExtensionDir: extensionDir,
		Display:      display,
		VNCPort:      vncPort,
		DebugPort:    debugPort,
		Chromium:     chromiumPath,
		LoginURL:     loginSessionLoginURL,
		StartedAt:    time.Now().UTC(),
		state:        LoginSessionStarting,
		message:      "正在启动远程登录窗口...",
		done:         make(chan struct{}),
	}
	debugf("login session allocated: id=%s username=%s password_set=%t display=%d vnc_port=%d debug_port=%d chromium=%s profile_dir=%s extension_dir=%s", sess.ID, username, strings.TrimSpace(password) != "", display, vncPort, debugPort, chromiumPath, profileDir, extensionDir)
	return sess, nil
}

func newDesktopSession() (*LoginSession, error) {
	sess, err := newLoginSession("", "")
	if err != nil {
		return nil, err
	}
	sess.ID = randID("desktop")
	sess.SessionKind = "desktop"
	sess.CaptureToken = false
	sess.LoginURL = loginSessionDesktopURL
	sess.message = "正在启动服务器远程桌面..."
	debugf("desktop session converted from login session: id=%s display=%d vnc_port=%d debug_port=%d chromium=%s", sess.ID, sess.Display, sess.VNCPort, sess.DebugPort, sess.Chromium)
	return sess, nil
}

func (s *LoginSession) start() error {
	debugf("login session start requested: id=%s kind=%s capture_token=%t login_url=%s", s.ID, s.SessionKind, s.CaptureToken, s.LoginURL)
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

	if s.CaptureToken {
		s.setRunning("远程登录窗口已打开，请在弹出的窗口中完成 Truth Social 登录。")
	} else {
		s.setRunning("服务器远程桌面已打开，请在远程桌面中的浏览器里手动登录。")
	}
	if s.CaptureToken && strings.TrimSpace(s.Username) != "" && strings.TrimSpace(s.Password) != "" {
		go s.runCredentialLogin()
	}
	debugf("login session start finished: id=%s kind=%s state=%s", s.ID, s.SessionKind, s.State())
	return nil
}

func (s *LoginSession) startXvfb() error {
	displayArg := ":" + strconv.Itoa(s.Display)
	cmd := exec.Command("Xvfb", displayArg, "-screen", "0", fmt.Sprintf("%dx%dx24", loginSessionWidth, loginSessionHeight), "-ac", "-nolisten", "tcp")
	debugf("login session starting Xvfb: id=%s cmd=%q args=%q", s.ID, cmd.Path, cmd.Args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.xvfbCmd = cmd
	debugf("login session Xvfb started: id=%s pid=%d display=%s", s.ID, cmd.Process.Pid, displayArg)
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
	debugf("login session starting x11vnc: id=%s cmd=%q args=%q", s.ID, cmd.Path, cmd.Args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DISPLAY="+displayArg)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.x11vncCmd = cmd
	debugf("login session x11vnc started: id=%s pid=%d display=%s port=%d", s.ID, cmd.Process.Pid, displayArg, s.VNCPort)
	go s.waitOnProcess("x11vnc", cmd)
	return nil
}

func (s *LoginSession) startChromium() error {
	displayArg := ":" + strconv.Itoa(s.Display)
	runtimeDir := filepath.Join(os.TempDir(), "truthsocial-runtime-"+s.ID)
	if !filepath.IsAbs(runtimeDir) {
		var err error
		runtimeDir, err = filepath.Abs(runtimeDir)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	log.Printf("truthsocial %s browser ready: session=%s profile_dir=%s debug_port=%d", s.SessionKind, s.ID, s.ProfileDir, s.DebugPort)
	debugf("login session runtime prepared: id=%s runtime_dir=%s display=%s chromium=%s", s.ID, runtimeDir, displayArg, s.Chromium)

	args := []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--disable-blink-features=AutomationControlled",
		"--disable-features=VizDisplayCompositor",
		"--exclude-switches=enable-automation",
		"--ozone-platform=x11",
		"--use-gl=swiftshader",
		"--force-device-scale-factor=1",
		"--window-position=0,0",
		"--window-size=" + strconv.Itoa(loginSessionWidth) + "," + strconv.Itoa(loginSessionHeight),
		"--user-data-dir=" + s.ProfileDir,
		"--remote-debugging-address=" + loginSessionBrowserAddress,
		"--remote-debugging-port=" + strconv.Itoa(s.DebugPort),
		"--no-sandbox",
		"--new-window",
		s.LoginURL,
	}
	var cmd *exec.Cmd
	useDBusRunSession := shouldUseDBusRunSession(s.Chromium)
	debugf("login session chromium launch mode: id=%s use_dbus_run_session=%t", s.ID, useDBusRunSession)
	if useDBusRunSession {
		args = append([]string{"--", s.Chromium}, args...)
		cmd = exec.Command("dbus-run-session", args...)
	} else {
		cmd = exec.Command(s.Chromium, args...)
	}
	debugf("login session starting chromium: id=%s cmd=%q args=%q", s.ID, cmd.Path, cmd.Args)
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
	debugf("login session chromium started: id=%s pid=%d", s.ID, cmd.Process.Pid)
	go s.waitOnProcess("chromium", cmd)
	return nil
}

func (s *LoginSession) waitOnProcess(name string, cmd *exec.Cmd) {
	debugf("login session process wait begin: session=%s process=%s pid=%d", s.ID, name, cmd.Process.Pid)
	if err := cmd.Wait(); err != nil {
		debugf("login session process exited: session=%s process=%s err=%v", s.ID, name, err)
		return
	}
	debugf("login session process exited cleanly: session=%s process=%s", s.ID, name)
}

func (s *LoginSession) monitor() {
	defer func() {
		s.markClosed("登录会话已结束。")
	}()

	deadline := time.Now().Add(loginSessionTimeout)
	lastProfileCaptureAttempt := time.Time{}
	lastProfileCaptureError := ""
	for {
		select {
		case <-s.done:
			return
		default:
		}

		if time.Now().After(deadline) {
			debugf("login session monitor timeout: session=%s deadline=%s", s.ID, deadline.Format(time.RFC3339))
			s.setError(errors.New("登录会话超时，请重新打开登录窗口。"))
			return
		}

		if s.CaptureToken && time.Since(lastProfileCaptureAttempt) >= loginSessionProfilePoll {
			lastProfileCaptureAttempt = time.Now()
			debugf("login session capture tick: session=%s elapsed=%s", s.ID, time.Since(s.StartedAt).Round(time.Second))
			captured, err := s.tryCaptureFromProfile()
			if captured {
				lastProfileCaptureError = ""
			} else if err != nil {
				errMsg := err.Error()
				if errMsg != lastProfileCaptureError {
					debugf("login session profile capture pending: session=%s err=%v", s.ID, err)
					lastProfileCaptureError = errMsg
				}
			}
		}

		switch s.State() {
		case LoginSessionSuccess:
			go func(id string) {
				time.Sleep(loginSessionCleanupDelay)
				s.cleanup()
				if s.manager != nil {
					s.manager.remove(id)
				}
			}(s.ID)
			return
		case LoginSessionError, LoginSessionClosed:
			return
		default:
			time.Sleep(loginSessionPollInterval)
		}
	}
}

func (s *LoginSession) runCredentialLogin() {
	debugf("login session credential automation scheduled: session=%s username=%s", s.ID, s.Username)
	// 等待 90 秒再启动，留时间让用户手动通过 Cloudflare 人机验证并填写账号密码
	select {
	case <-time.After(90 * time.Second):
	case <-s.done:
		debugf("login session credential automation stopped during warmup: session=%s", s.ID)
		return
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-s.done:
			debugf("login session credential automation stopped: session=%s reason=done", s.ID)
			return
		default:
		}

		state := s.State()
		if state == LoginSessionSuccess || state == LoginSessionError || state == LoginSessionClosed {
			debugf("login session credential automation stopped: session=%s state=%s", s.ID, state)
			return
		}

		if s.DebugPort <= 0 {
			debugf("login session credential automation waiting for debug port: session=%s", s.ID)
			time.Sleep(5 * time.Second)
			continue
		}

		submitted, pageURL, err := submitTruthSocialCredentialsViaDebugPort(s.DebugPort, s.Username, s.Password)
		if err != nil {
			debugf("login session credential automation pending: session=%s err=%v", s.ID, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if submitted {
			log.Printf("truthsocial credential login submitted: session=%s page=%s", s.ID, pageURL)
			return
		}
		debugf("login session credential automation no form found yet: session=%s page=%s", s.ID, pageURL)
		time.Sleep(5 * time.Second)
	}
	debugf("login session credential automation timeout: session=%s", s.ID)
}

func (s *LoginSession) tryCaptureFromProfile() (bool, error) {
	debugf("login session capture begin: session=%s", s.ID)
	token, cookies, err := s.attachAndReadCookieData()
	if err != nil {
		debugf("login session capture read failed: session=%s err=%v", s.ID, err)
		return false, err
	}

	token = strings.TrimSpace(token)
	if token == "" {
		debugf("login session capture empty token: session=%s cookies=%d", s.ID, len(cookies))
		return false, fmt.Errorf("token not found in browser profile yet")
	}

	if err := persistBearerToken(token); err != nil {
		debugf("login session persist bearer token failed: session=%s err=%v", s.ID, err)
		return false, err
	}
	if err := persistLoginSessionData(s.ID, s.Username, token, cookies); err != nil {
		log.Printf("truthsocial login profile capture persist data failed: session=%s err=%v", s.ID, err)
	}

	s.setSuccess(token, cookies)
	log.Printf("truthsocial login capture received: session=%s source=profile cookies=%d", s.ID, len(cookies))
	return true, nil
}

func (s *LoginSession) stop() {
	s.closeOnce.Do(func() {
		debugf("login session stop signalled: session=%s", s.ID)
		close(s.done)
	})
}

func (s *LoginSession) cleanup() {
	debugf("login session cleanup begin: session=%s", s.ID)
	s.stop()
	if s.chromeCmd != nil && s.chromeCmd.Process != nil {
		debugf("login session cleanup killing chromium: session=%s pid=%d", s.ID, s.chromeCmd.Process.Pid)
		_ = s.chromeCmd.Process.Kill()
	}
	if s.x11vncCmd != nil && s.x11vncCmd.Process != nil {
		debugf("login session cleanup killing x11vnc: session=%s pid=%d", s.ID, s.x11vncCmd.Process.Pid)
		_ = s.x11vncCmd.Process.Kill()
	}
	if s.xvfbCmd != nil && s.xvfbCmd.Process != nil {
		debugf("login session cleanup killing Xvfb: session=%s pid=%d", s.ID, s.xvfbCmd.Process.Pid)
		_ = s.xvfbCmd.Process.Kill()
	}
	_ = os.RemoveAll(s.ProfileDir)
	_ = os.RemoveAll(s.ExtensionDir)
	_ = os.RemoveAll(filepath.Join(os.TempDir(), "truthsocial-runtime-"+s.ID))
	debugf("login session cleanup paths removed: session=%s profile_dir=%s extension_dir=%s", s.ID, s.ProfileDir, s.ExtensionDir)
	s.markClosed("登录会话已关闭。")
}

func (s *LoginSession) setRunning(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionRunning
	s.message = message
	debugf("login session state changed: session=%s state=%s message=%s", s.ID, s.state, message)
}

func (s *LoginSession) setSuccess(token string, cookies []CapturedCookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionSuccess
	if s.CaptureToken {
		s.message = "登录完成，Bearer Token 已写回后端。"
	} else {
		s.message = "服务器远程桌面会话运行中。"
	}
	s.token = token
	s.cookies = cookies
	debugf("login session state changed: session=%s state=%s token=%s cookies=%d", s.ID, s.state, maskToken(token), len(cookies))
}

func (s *LoginSession) setToken(token string, cookies []CapturedCookie) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	s.cookies = cookies
	debugf("login session token updated: session=%s token=%s cookies=%d", s.ID, maskToken(token), len(cookies))
}

func (s *LoginSession) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = LoginSessionError
	s.message = err.Error()
	debugf("login session state changed: session=%s state=%s error=%v", s.ID, s.state, err)
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
	debugf("login session marked closed: session=%s state=%s message=%s", s.ID, s.state, s.message)
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
		"extension_dir": s.ExtensionDir,
		"chromium_path": s.Chromium,
	}
}

func (s *LoginSession) attachAndReadCookieData() (string, []CapturedCookie, error) {
	if !s.CaptureToken {
		return "", nil, fmt.Errorf("token capture disabled for desktop session")
	}
	debugf("login session capture source select: session=%s debug_port=%d warmup_elapsed=%s", s.ID, s.DebugPort, time.Since(s.StartedAt).Round(time.Second))

	profileToken, profileCookies, profileErr := readTokenAndCookiesFromProfileDir(s.ProfileDir, s.LoginURL)
	profileToken = strings.TrimSpace(profileToken)
	if profileErr == nil && profileToken != "" {
		debugf("login session capture source hit profile: session=%s token=%s cookies=%d", s.ID, maskToken(profileToken), len(profileCookies))
		return profileToken, profileCookies, nil
	}

	if s.DebugPort <= 0 {
		debugf("login session capture source no debug port: session=%s profile_err=%v", s.ID, profileErr)
		if profileErr != nil {
			return "", nil, profileErr
		}
		return "", profileCookies, fmt.Errorf("token not found in browser profile yet")
	}
	if time.Since(s.StartedAt) < loginSessionDebugWarmup {
		debugf("login session capture source waiting for debug warmup: session=%s warmup=%s", s.ID, loginSessionDebugWarmup)
		if profileErr != nil {
			return "", nil, profileErr
		}
		return "", profileCookies, fmt.Errorf("token not found in browser profile yet")
	}

	debugToken, debugCookies, debugErr := readTokenAndCookiesFromDebugPort(s.DebugPort, s.LoginURL)
	debugToken = strings.TrimSpace(debugToken)
	if debugErr == nil && debugToken != "" {
		debugf("login session capture source hit remote debug: session=%s token=%s cookies=%d", s.ID, maskToken(debugToken), len(debugCookies))
		return debugToken, debugCookies, nil
	}

	if profileErr == nil {
		return "", profileCookies, fmt.Errorf("token not found in browser profile yet")
	}
	if debugErr != nil {
		return "", nil, fmt.Errorf("%v; remote debug fallback pending: %w", profileErr, debugErr)
	}
	return "", nil, profileErr
}

func shouldUseDBusRunSession(browserPath string) bool {
	if strings.TrimSpace(browserPath) == "" {
		debugf("login session dbus wrapper skipped: empty browser path")
		return false
	}
	normalized := filepath.Clean(strings.ToLower(browserPath))
	if strings.Contains(normalized, "/snap/bin/") || strings.HasPrefix(normalized, `\snap\bin\`) {
		debugf("login session dbus wrapper skipped for snap browser: browser=%s", browserPath)
		return false
	}
	if !strings.Contains(strings.ToLower(browserPath), "chromium") {
		debugf("login session dbus wrapper skipped: non-chromium browser=%s", browserPath)
		return false
	}
	if _, err := exec.LookPath("dbus-run-session"); err != nil {
		debugf("login session dbus wrapper unavailable: browser=%s err=%v", browserPath, err)
		return false
	}
	debugf("login session dbus wrapper enabled: browser=%s", browserPath)
	return true
}

func (s *LoginSession) ensureCaptureExtension() error {
	if err := os.MkdirAll(s.ExtensionDir, 0o700); err != nil {
		return err
	}

	captureURL := "http://127.0.0.1:8085/truthsocial/login/session/" + url.PathEscape(s.ID) + "/capture"
	manifest := `{
  "manifest_version": 3,
  "name": "Truth Social Login Capture",
  "version": "1.0.0",
  "permissions": ["cookies", "storage", "webRequest"],
  "host_permissions": ["https://truthsocial.com/*", "http://127.0.0.1:8085/*"],
  "background": {"service_worker": "background.js"},
  "content_scripts": [{
    "matches": ["https://truthsocial.com/*"],
    "js": ["content.js"],
    "run_at": "document_idle"
  }]
}`
	background := `importScripts('config.js');

let capturedToken = '';
let captureInFlight = false;

async function getCookies() {
  return await new Promise((resolve) => {
    chrome.cookies.getAll({ url: 'https://truthsocial.com/' }, (items) => {
      const rows = (items || []).map((c) => ({
        name: c.name || '',
        value: c.value || '',
        domain: c.domain || '',
        path: c.path || '',
        expires: c.expirationDate || 0,
        http_only: !!c.httpOnly,
        secure: !!c.secure,
        same_site: c.sameSite || '',
        priority: c.priority || ''
      }));
      resolve(rows);
    });
  });
}

async function sendCapture(token, source, pageUrl) {
  const normalized = (token || '').trim();
  if (!normalized) {
    return { ok: false, error: 'empty token' };
  }
  if (normalized === capturedToken || captureInFlight) {
    return { ok: true, skipped: true };
  }

  captureInFlight = true;
  try {
    const payload = {
      session_id: TRUTHSOCIAL_LOGIN_SESSION_ID,
      username: TRUTHSOCIAL_LOGIN_USERNAME,
      bearer_token: normalized,
      cookies: await getCookies(),
      page_url: pageUrl || '',
      source: source || 'page',
      captured_at: new Date().toISOString()
    };
    const resp = await fetch(TRUTHSOCIAL_LOGIN_CAPTURE_URL, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(payload)
    });
    const text = await resp.text();
    if (resp.ok) {
      capturedToken = normalized;
    }
    return { ok: resp.ok, status: resp.status, body: text };
  } finally {
    captureInFlight = false;
  }
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (!msg || msg.type !== 'truthsocial-capture' || !msg.token) {
    return;
  }

  (async () => {
    const resp = await sendCapture(msg.token, 'content-script', msg.pageUrl || '');
    sendResponse(resp);
  })().catch((err) => {
    sendResponse({ ok: false, error: String(err) });
  });
  return true;
});

chrome.webRequest.onBeforeSendHeaders.addListener(
  (details) => {
    try {
      const headers = details && Array.isArray(details.requestHeaders) ? details.requestHeaders : [];
      for (const header of headers) {
        const name = String(header && header.name ? header.name : '').toLowerCase();
        if (name !== 'authorization') {
          continue;
        }
        const value = String(header && header.value ? header.value : '').trim();
        if (!/^bearer\s+/i.test(value)) {
          continue;
        }
        const token = value.replace(/^bearer\s+/i, '').trim();
        if (!token) {
          continue;
        }
        sendCapture(token, 'web-request', details && details.url ? details.url : '').catch((err) => {
          console.warn('truthsocial login webRequest capture failed', err);
        });
        break;
      }
    } catch (err) {
      console.warn('truthsocial login webRequest handler failed', err);
    }
  },
  { urls: ['https://truthsocial.com/*'] },
  ['requestHeaders', 'extraHeaders']
);
`
	content := fmt.Sprintf(`(() => {
%s
  const sentKey = 'truthsocial_capture_sent_%s';

  function sendCapture(token) {
    return new Promise((resolve, reject) => {
      chrome.runtime.sendMessage(
        { type: 'truthsocial-capture', token: token, pageUrl: location.href },
        (resp) => {
          if (chrome.runtime.lastError) {
            reject(new Error(chrome.runtime.lastError.message));
            return;
          }
          if (resp && resp.ok) {
            resolve(resp);
            return;
          }
          reject(new Error((resp && (resp.error || resp.body)) || 'capture failed'));
        }
      );
    });
  }

  async function tick() {
    if (sessionStorage.getItem(sentKey) === '1') {
      return;
    }
    const token = readTruthSocialBearerToken();
    if (!token) {
      return;
    }
    try {
      await sendCapture(token);
      sessionStorage.setItem(sentKey, '1');
    } catch (err) {
      console.warn('truthsocial login capture failed', err);
    }
  }

  tick();
  setInterval(tick, 1000);
})();
`, browserTokenDiscoveryJS(), s.ID)
	config := fmt.Sprintf(`self.TRUTHSOCIAL_LOGIN_SESSION_ID = %q;
self.TRUTHSOCIAL_LOGIN_USERNAME = %q;
self.TRUTHSOCIAL_LOGIN_CAPTURE_URL = %q;
`, s.ID, s.Username, captureURL)

	files := map[string]string{
		filepath.Join(s.ExtensionDir, "manifest.json"): manifest,
		filepath.Join(s.ExtensionDir, "background.js"): background,
		filepath.Join(s.ExtensionDir, "content.js"):    content,
		filepath.Join(s.ExtensionDir, "config.js"):     config,
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func persistLoginSessionData(sessionID, username, token string, cookies []CapturedCookie) error {
	payload := map[string]any{
		"session_id":   sessionID,
		"username":     username,
		"bearer_token": token,
		"cookies":      cookies,
		"captured_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("truthsocial_login_session.json", data, 0o600)
}

func readTokenAndCookiesFromProfileDir(userDataDir, loginURL string) (string, []CapturedCookie, error) {
	account := extractUsernameFromEntry(loginURL)
	var token string
	var cookies []CapturedCookie
	debugf("profile token capture begin: account=%s profile_dir=%s login_url=%s", account, userDataDir, loginURL)

	err := runBrowserTaskWithProfileFallback(userDataDir, func(ctx context.Context) error {
		if err := chromedp.Run(ctx,
			chromedp.Navigate(loginURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				return chromedp.Evaluate(`(function() {
					`+browserTokenDiscoveryJS()+`
					return readTruthSocialBearerToken();
				})()`, &token).Do(ctx)
			}),
		); err != nil {
			return err
		}

		cookieRows, err := network.GetCookies().WithUrls([]string{loginURL, "https://truthsocial.com/"}).Do(ctx)
		if err != nil {
			return err
		}

		captured := make([]CapturedCookie, 0, len(cookieRows))
		for _, c := range cookieRows {
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
		cookies = captured
		return nil
	})
	if err != nil {
		debugf("profile token capture failed: account=%s profile_dir=%s err=%v", account, userDataDir, err)
		return "", nil, fmt.Errorf("profile token capture failed for %s: %w", account, err)
	}
	token = strings.TrimSpace(token)
	debugf("profile token capture finished: account=%s token=%s cookies=%d", account, maskToken(token), len(cookies))
	return token, cookies, nil
}

func readTokenAndCookiesFromDebugPort(debugPort int, loginURL string) (string, []CapturedCookie, error) {
	account := extractUsernameFromEntry(loginURL)
	if debugPort <= 0 {
		return "", nil, fmt.Errorf("remote debug port is unavailable")
	}
	debugf("remote debug token capture begin: account=%s debug_port=%d login_url=%s", account, debugPort, loginURL)

	allocatorURL := fmt.Sprintf("http://%s:%d", loginSessionBrowserAddress, debugPort)
	debugf("remote debug allocator url: account=%s url=%s", account, allocatorURL)
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), allocatorURL)
	defer allocCancel()

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, 45*time.Second)
	defer timeoutCancel()

	targetID, err := selectTruthSocialTargetID(ctx)
	if err != nil {
		debugf("remote debug token capture target selection failed: account=%s err=%v", account, err)
		return "", nil, fmt.Errorf("remote debug target selection failed for %s: %w", account, err)
	}
	debugf("remote debug token capture target selected: account=%s target_id=%s", account, targetID)

	targetCtx, targetCancel := chromedp.NewContext(ctx, chromedp.WithTargetID(targetID))
	defer targetCancel()

	var token string
	var cookies []CapturedCookie
	err = chromedp.Run(targetCtx,
		network.Enable(),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Evaluate(`(function() {
				`+browserTokenDiscoveryJS()+`
				return readTruthSocialBearerToken();
			})()`, &token).Do(ctx); err != nil {
				return err
			}

			cookieRows, err := network.GetCookies().WithUrls([]string{loginURL, "https://truthsocial.com/"}).Do(ctx)
			if err != nil {
				return err
			}

			captured := make([]CapturedCookie, 0, len(cookieRows))
			for _, c := range cookieRows {
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
			cookies = captured
			return nil
		}),
	)
	if err != nil {
		debugf("remote debug token capture failed: account=%s debug_port=%d err=%v", account, debugPort, err)
		return "", nil, fmt.Errorf("remote debug token capture failed for %s: %w", account, err)
	}
	token = strings.TrimSpace(token)
	debugf("remote debug token capture finished: account=%s token=%s cookies=%d", account, maskToken(token), len(cookies))
	return token, cookies, nil
}

func submitTruthSocialCredentialsViaDebugPort(debugPort int, username, password string) (bool, string, error) {
	if debugPort <= 0 {
		return false, "", fmt.Errorf("remote debug port is unavailable")
	}
	allocatorURL := fmt.Sprintf("http://%s:%d", loginSessionBrowserAddress, debugPort)
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), allocatorURL)
	defer allocCancel()

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	defer ctxCancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, 20*time.Second)
	defer timeoutCancel()

	targetID, err := selectTruthSocialTargetID(ctx)
	if err != nil {
		return false, "", fmt.Errorf("remote debug target selection failed: %w", err)
	}

	targetCtx, targetCancel := chromedp.NewContext(ctx, chromedp.WithTargetID(targetID))
	defer targetCancel()

	var result struct {
		Submitted bool   `json:"submitted"`
		PageURL   string `json:"page_url"`
		Reason    string `json:"reason"`
	}
	js := fmt.Sprintf(`(() => {
  const username = %q;
  const password = %q;

  const pageURL = location.href || '';
  // 不在 truthsocial.com 则跳过
  if (!pageURL.includes('truthsocial.com')) {
    return { submitted: false, page_url: pageURL, reason: 'not on truthsocial.com' };
  }
  // 检测到 Cloudflare 人机验证则跳过
  if (document.querySelector('#challenge-form, .cf-challenge-running, #cf-wrapper, [data-translate="checking_browser"], .cf-turnstile, iframe[src*="challenges.cloudflare.com"]')) {
    return { submitted: false, page_url: pageURL, reason: 'cloudflare challenge active' };
  }

  const setValue = (el, value) => {
    const proto = Object.getPrototypeOf(el);
    const descriptor = Object.getOwnPropertyDescriptor(proto, 'value');
    if (descriptor && typeof descriptor.set === 'function') {
      descriptor.set.call(el, value);
    } else {
      el.value = value;
    }
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
  };

  const userInput = document.querySelector('input[name=\"username\"], input[autocomplete=\"username\"], input[type=\"email\"], input[type=\"text\"]');
  const passInput = document.querySelector('input[name=\"password\"], input[autocomplete=\"current-password\"], input[type=\"password\"]');
  if (!userInput || !passInput) {
    return { submitted: false, page_url: pageURL, reason: 'login form not found' };
  }

  // 如果用户已经开始手动输入，跳过自动填写
  if ((userInput.value || '').trim() !== '' || (passInput.value || '').trim() !== '') {
    return { submitted: false, page_url: pageURL, reason: 'user already typing' };
  }

  setValue(userInput, username);
  setValue(passInput, password);

  const candidates = Array.from(document.querySelectorAll('button, input[type=\"submit\"]'));
  const submitButton = candidates.find((el) => {
    const text = (el.innerText || el.value || '').trim().toLowerCase();
    return /sign in|log in|login|继续|continue|submit/.test(text);
  }) || userInput.form?.querySelector('button[type=\"submit\"], input[type=\"submit\"]') || passInput.form?.querySelector('button[type=\"submit\"], input[type=\"submit\"]');

  if (submitButton) {
    submitButton.click();
    return { submitted: true, page_url: pageURL, reason: 'clicked submit button' };
  }

  if (passInput.form && typeof passInput.form.requestSubmit === 'function') {
    passInput.form.requestSubmit();
    return { submitted: true, page_url: pageURL, reason: 'requestSubmit' };
  }

  passInput.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true }));
  passInput.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', code: 'Enter', bubbles: true }));
  return { submitted: true, page_url: pageURL, reason: 'keyboard enter' };
})()`, username, password)

	if err := chromedp.Run(targetCtx, chromedp.Evaluate(js, &result)); err != nil {
		return false, "", fmt.Errorf("credential submit failed: %w", err)
	}
	if result.Reason != "" {
		debugf("truthsocial credential submit result: debug_port=%d submitted=%t page=%s reason=%s", debugPort, result.Submitted, result.PageURL, result.Reason)
	}
	return result.Submitted, result.PageURL, nil
}

func selectTruthSocialTargetID(ctx context.Context) (target.ID, error) {
	targets, err := chromedp.Targets(ctx)
	if err != nil {
		return "", err
	}
	debugf("remote debug target scan: targets=%d", len(targets))

	var fallback target.ID
	for _, info := range targets {
		if info == nil || info.Type != "page" {
			continue
		}
		debugf("remote debug target candidate: target_id=%s url=%s type=%s", info.TargetID, info.URL, info.Type)
		if fallback == "" {
			fallback = info.TargetID
		}
		url := strings.ToLower(strings.TrimSpace(info.URL))
		if strings.Contains(url, "truthsocial.com") {
			return info.TargetID, nil
		}
	}
	if fallback != "" {
		debugf("remote debug target fallback selected: target_id=%s", fallback)
		return fallback, nil
	}
	return "", fmt.Errorf("no page target found")
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
		debugf("x11vnc already installed")
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
	debugf("login session websocket proxy start: session=%s vnc_port=%d remote=%s", s.ID, s.VNCPort, r.RemoteAddr)
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
	debugf("login session websocket proxy connected: session=%s backend=%s:%d", s.ID, loginSessionVNCListenHost, s.VNCPort)
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
