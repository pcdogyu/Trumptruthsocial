package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const contentPageSize = 10

type App struct {
	store         *PostStore
	templates     map[string]*template.Template
	gitInfo       GitInfo
	loginSessions *LoginSessionManager
}

func newApp(store *PostStore) (*App, error) {
	files := []string{
		filepath.Join("templates", "index.html"),
		filepath.Join("templates", "content.html"),
		filepath.Join("templates", "message_push.html"),
		filepath.Join("templates", "ai_config.html"),
		filepath.Join("templates", "translation_config.html"),
		filepath.Join("templates", "history.html"),
		filepath.Join("templates", "login_session.html"),
	}

	templates := make(map[string]*template.Template, len(files))
	for _, file := range files {
		tpl, err := template.ParseFiles(file)
		if err != nil {
			return nil, err
		}
		templates[filepath.Base(file)] = tpl
	}

	return &App{
		store:         store,
		templates:     templates,
		gitInfo:       getGitCommitInfo(),
		loginSessions: newLoginSessionManager(),
	}, nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/save_config", a.handleSaveConfig)
	mux.HandleFunc("/content", a.handleContent)
	mux.HandleFunc("/content/", a.handleContent)
	mux.HandleFunc("/delete_post/", a.handleDeletePost)
	mux.HandleFunc("/block_post/", a.handleBlockPost)
	mux.HandleFunc("/forward_post/", a.handleForwardPost)
	mux.HandleFunc("/translate_post/", a.handleTranslatePost)
	mux.HandleFunc("/posts/bulk_action", a.handleBulkPostsAction)
	mux.HandleFunc("/sync_content", a.handleSyncContent)
	mux.HandleFunc("/sync_latest_post", a.handleSyncLatestPost)
	mux.HandleFunc("/upgrade", a.handleUpgrade)
	mux.HandleFunc("/upgrade/log", a.handleUpgradeLog)
	mux.HandleFunc("/ai_config", a.handleAIConfig)
	mux.HandleFunc("/ai_config/save", a.handleSaveAIConfig)
	mux.HandleFunc("/translation_config", a.handleTranslationConfig)
	mux.HandleFunc("/translation_config/save", a.handleSaveTranslationConfig)
	mux.HandleFunc("/message_push", a.handleMessagePush)
	mux.HandleFunc("/message_push/save", a.handleMessagePushSave)
	mux.HandleFunc("/message_push/test", a.handleMessagePushTest)
	mux.HandleFunc("/truthsocial/login", a.handleTruthSocialLogin)
	mux.HandleFunc("/desktop/start", a.handleDesktopStart)
	mux.HandleFunc("/truthsocial/login/session/", a.handleTruthSocialLoginSession)
	mux.HandleFunc("/config_page", a.handleConfigPage)
	return mux
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	tpl, ok := a.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _ := LoadConfig()
	data := IndexPageData{
		Title:                    "配置",
		ActivePage:               "config",
		Git:                      a.gitInfo,
		Config:                   cfg,
		AccountsText:             strings.Join(cfg.AccountsToMonitor, "\n"),
		BearerTokenValue:         maskSecret(cfg.Auth.BearerToken),
		TruthSocialPasswordValue: secretOrBlank(cfg.Auth.TruthSocialPassword),
		AIApiKeyValue:            secretOrBlank(cfg.AIAnalysis.APIKey),
	}
	if r.URL.Query().Get("saved") != "" {
		data.Message = "配置已保存。"
	}
	a.render(w, "index.html", data)
}

func (a *App) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	cfg, _ := LoadConfig()
	bearerToken := strings.TrimSpace(r.FormValue("bearer_token"))
	if bearerToken != "" {
		if bearerToken == maskSecret(cfg.Auth.BearerToken) {
			bearerToken = cfg.Auth.BearerToken
		}
	}
	rotateBearerTokens(&cfg, bearerToken)
	cfg.Auth.TruthSocialUsername = strings.TrimSpace(r.FormValue("truthsocial_username"))
	cfg.Auth.TruthSocialPassword = r.FormValue("truthsocial_password")
	if validityDays, err := strconv.Atoi(strings.TrimSpace(r.FormValue("bearer_token_validity_days"))); err == nil {
		cfg.Auth.BearerTokenValidityDays = validityDays
	}
	cfg.RefreshInterval = strings.TrimSpace(r.FormValue("refresh_interval"))
	cfg.AccountsToMonitor = splitLines(r.FormValue("accounts_to_monitor"))
	if cfg.RefreshInterval == "" {
		cfg.RefreshInterval = "5m"
	}

	if err := SaveConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("config saved, accounts=%d", len(cfg.AccountsToMonitor))
	http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
}

func (a *App) handleTruthSocialLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg, _ := LoadConfig()
	username := strings.TrimSpace(r.FormValue("truthsocial_username"))
	if username == "" {
		username = strings.TrimSpace(cfg.Auth.TruthSocialUsername)
	}
	password := r.FormValue("truthsocial_password")
	if password == "" {
		password = cfg.Auth.TruthSocialPassword
	}
	log.Printf("truthsocial login requested via web ui: username=@%s", username)

	session, err := a.loginSessions.Start(username, password)
	if err != nil {
		log.Printf("truthsocial login session start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": err.Error()})
		return
	}

	log.Printf("truthsocial login session started: id=%s username=@%s display=%d vnc_port=%d debug_port=%d", session.ID, session.Username, session.Display, session.VNCPort, session.DebugPort)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"message":    "远程登录窗口已打开，请在新窗口完成 Truth Social 登录。",
		"session_id": session.ID,
		"viewer_url": a.loginSessionViewerURL(r, session.ID),
		"status_url": a.loginSessionStatusURL(r, session.ID),
	})
}

func (a *App) handleDesktopStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Printf("desktop session requested via web ui")
	session, err := a.loginSessions.StartDesktop()
	if err != nil {
		log.Printf("desktop session start failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": err.Error()})
		return
	}

	log.Printf("desktop session started: id=%s display=%d vnc_port=%d debug_port=%d", session.ID, session.Display, session.VNCPort, session.DebugPort)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"message":    "服务器远程桌面已打开，请在新窗口中操作浏览器。",
		"session_id": session.ID,
		"viewer_url": a.loginSessionViewerURL(r, session.ID),
		"status_url": a.loginSessionStatusURL(r, session.ID),
	})
}

func (a *App) handleTruthSocialLoginSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodConnect {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	suffix := strings.TrimPrefix(r.URL.Path, "/truthsocial/login/session/")
	suffix = strings.Trim(suffix, "/")
	if suffix == "" {
		http.NotFound(w, r)
		return
	}

	if strings.HasSuffix(suffix, "/status") {
		id := strings.TrimSuffix(suffix, "/status")
		id = strings.Trim(id, "/")
		a.handleTruthSocialLoginSessionStatus(w, r, id)
		return
	}

	if strings.HasSuffix(suffix, "/vnc") {
		id := strings.TrimSuffix(suffix, "/vnc")
		id = strings.Trim(id, "/")
		a.handleTruthSocialLoginSessionVNC(w, r, id)
		return
	}

	if strings.HasSuffix(suffix, "/capture") {
		id := strings.TrimSuffix(suffix, "/capture")
		id = strings.Trim(id, "/")
		a.handleTruthSocialLoginSessionCapture(w, r, id)
		return
	}

	session, ok := a.loginSessions.Get(suffix)
	if !ok {
		http.NotFound(w, r)
		return
	}

	snapshot := session.Snapshot()
	message, _ := snapshot["message"].(string)
	title := "Truth Social 登录会话"
	if session.SessionKind == "desktop" {
		title = "服务器远程桌面"
	}
	data := LoginSessionPageData{
		Title:       title,
		ActivePage:  "config",
		Git:         a.gitInfo,
		SessionID:   session.ID,
		Username:    session.Username,
		Message:     message,
		SessionKind: session.SessionKind,
	}
	a.render(w, "login_session.html", data)
}

func (a *App) handleTruthSocialLoginSessionStatus(w http.ResponseWriter, r *http.Request, sessionID string) {
	session, ok := a.loginSessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "登录会话不存在或已结束。"})
		return
	}

	snapshot := session.Snapshot()
	if state, ok := snapshot["state"].(string); ok && state != "" {
		snapshot["status"] = state
	} else {
		snapshot["status"] = "running"
	}
	snapshot["viewer_url"] = a.loginSessionViewerURL(r, sessionID)
	snapshot["vnc_url"] = a.loginSessionWebSocketURL(r, sessionID)
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) handleTruthSocialLoginSessionVNC(w http.ResponseWriter, r *http.Request, sessionID string) {
	session, ok := a.loginSessions.Get(sessionID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	session.VNCWebSocketHandler(w, r)
}

func (a *App) handleTruthSocialLoginSessionCapture(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, ok := a.loginSessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"status": "error", "message": "登录会话不存在或已结束。"})
		return
	}

	var payload struct {
		BearerToken string           `json:"bearer_token"`
		Cookies     []CapturedCookie `json:"cookies"`
		PageURL     string           `json:"page_url"`
		Source      string           `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "捕获数据解析失败。"})
		return
	}

	token := strings.TrimSpace(payload.BearerToken)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "未收到 Bearer Token。"})
		return
	}

	if err := persistBearerToken(token); err != nil {
		log.Printf("truthsocial login capture persist token failed: session=%s err=%v", sessionID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": err.Error()})
		return
	}

	if err := persistLoginSessionData(session.ID, session.Username, token, payload.Cookies); err != nil {
		log.Printf("truthsocial login capture persist data failed: session=%s err=%v", sessionID, err)
	}

	session.setSuccess(token, payload.Cookies)
	source := strings.TrimSpace(payload.Source)
	if source == "" {
		source = "unknown"
	}
	log.Printf("truthsocial login capture received: session=%s source=%s cookies=%d", sessionID, source, len(payload.Cookies))
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Bearer Token 已写回后端。"})
}

func (a *App) handleContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	selected := ""
	if r.URL.Path != "/content" && r.URL.Path != "/content/" {
		selected = strings.TrimPrefix(r.URL.Path, "/content/")
		selected = strings.Trim(selected, "/")
		if decoded, err := url.PathUnescape(selected); err == nil {
			selected = decoded
		}
	}
	page := parsePageNumber(r.URL.Query().Get("page"))
	totalPosts := len(a.store.GetAllPosts(selected, 0, 0))
	totalPages := totalPagesFor(totalPosts, contentPageSize)
	if totalPages == 0 {
		page = 1
	} else if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * contentPageSize
	posts := a.store.GetAllPosts(selected, contentPageSize, offset)

	data := ContentPageData{
		Title:            "历史内容",
		ActivePage:       "content",
		Git:              a.gitInfo,
		Posts:            posts,
		Usernames:        a.store.GetUsernames(),
		SelectedUsername: selected,
		CurrentPage:      page,
		PageSize:         contentPageSize,
		TotalPosts:       totalPosts,
		TotalPages:       totalPages,
		PaginationLinks:  buildPaginationLinks(contentBaseURL(selected), page, totalPages),
	}
	a.render(w, "content.html", data)
}

func (a *App) handleDeletePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	postID := strings.TrimPrefix(r.URL.Path, "/delete_post/")
	postID = strings.TrimSpace(postID)
	if postID == "" {
		http.Error(w, "post id required", http.StatusBadRequest)
		return
	}

	ok, err := a.store.DeletePost(postID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("未在数据库中找到帖子 %s。", postID)})
		return
	}
	log.Printf("post deleted: %s", postID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": fmt.Sprintf("帖子 %s 已删除。", postID)})
}

func (a *App) handleBlockPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	postID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/block_post/"))
	if postID == "" {
		http.Error(w, "post id required", http.StatusBadRequest)
		return
	}

	ok, err := a.store.SetStatus(postID, PostStatusBlocked)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("未在数据库中找到帖子 %s。", postID)})
		return
	}
	log.Printf("post blocked: %s", postID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": fmt.Sprintf("帖子 %s 已屏蔽。", postID)})
}

func (a *App) handleForwardPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	postID := strings.TrimPrefix(r.URL.Path, "/forward_post/")
	postID = strings.TrimSpace(postID)
	if postID == "" {
		http.Error(w, "post id required", http.StatusBadRequest)
		return
	}

	post, ok := a.store.GetPostByID(postID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "帖子未找到。"})
		return
	}
	debugf("HTTP forward request: post_id=%s username=@%s image=%t video=%t", post.ID, post.Username, strings.TrimSpace(post.ImageURL) != "", strings.TrimSpace(post.VideoURL) != "")
	if post.Status == PostStatusBlocked {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "该帖子已被屏蔽，不能转发。"})
		return
	}
	cfg, _ := LoadConfig()
	enrichPostTranslation(cfg, &post, true)
	if post.TranslatedAt != "" || post.TranslationError != "" {
		if _, err := a.store.UpdatePostTranslation(post.ID, post.TranslatedContent, post.TranslatedAt, post.TranslationError); err != nil {
			log.Printf("persist translation failed for %s: %v", post.ID, err)
		}
	}
	success, message := forwardPostToTelegram(cfg, post)
	if success {
		if updated, err := a.store.MarkSent(postID); err != nil {
			log.Printf("mark sent failed for %s: %v", postID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"message": "帖子已发送到 Telegram，但本地发送状态更新失败，请刷新后重试。",
			})
			return
		} else if !updated {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"message": "帖子已发送到 Telegram，但未能更新本地发送状态。",
			})
			return
		}
		log.Printf("post forwarded: %s", postID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"message": message})
}

func (a *App) handleTranslatePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	postID := strings.TrimPrefix(r.URL.Path, "/translate_post/")
	postID = strings.TrimSpace(postID)
	if postID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "post id required"})
		return
	}
	post, ok := a.store.GetPostByID(postID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "帖子未找到。"})
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "加载配置失败: " + err.Error()})
		return
	}
	enrichPostTranslation(cfg, &post, true)
	if post.TranslatedAt != "" || post.TranslationError != "" {
		if _, err := a.store.UpdatePostTranslation(post.ID, post.TranslatedContent, post.TranslatedAt, post.TranslationError); err != nil {
			log.Printf("persist translation failed for %s: %v", post.ID, err)
		}
	}
	if post.TranslationError != "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": post.TranslationError})
		return
	}
	if strings.TrimSpace(post.TranslatedContent) == "" {
		writeJSON(w, http.StatusOK, map[string]string{"message": "帖子内容仅含链接，无文字需要翻译。"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"translated_content": post.TranslatedContent})
}

func (a *App) handleSyncContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SyncRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	debugf("HTTP sync content request: days=%d", req.Days)
	added, err := syncAllAccounts(a.store, req.Days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, SyncResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("historical sync completed, days=%d, new_posts=%d", req.Days, added)
	writeJSON(w, http.StatusOK, SyncResponse{Status: "info", Message: fmt.Sprintf("历史同步完成，新增 %d 条帖子。", added), NewPosts: added})
}

func (a *App) handleSyncLatestPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	debugf("HTTP sync latest request received")
	added, err := syncLatestAccounts(a.store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, SyncResponse{Status: "error", Message: err.Error()})
		return
	}
	log.Printf("latest sync completed, new_posts=%d", added)
	writeJSON(w, http.StatusOK, SyncResponse{Status: "success", Message: fmt.Sprintf("最新帖子同步完成，新增 %d 条帖子。", added), NewPosts: added})
}

func (a *App) handleBulkPostsAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req BulkActionRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "请选择至少一条帖子。"})
		return
	}

	switch req.Action {
	case "delete":
		count, err := a.store.BulkDelete(req.IDs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("bulk delete posts: %d items", count)
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": fmt.Sprintf("已删除 %d 条帖子。", count), "count": count})
	case "block":
		count, err := a.store.BulkSetStatus(req.IDs, PostStatusBlocked)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("bulk block posts: %d items", count)
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": fmt.Sprintf("已屏蔽 %d 条帖子。", count), "count": count})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "未知操作类型。"})
	}
}

func (a *App) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _ := LoadConfig()
	data := AIConfigPageData{
		Title:         "AI 配置",
		ActivePage:    "ai",
		Git:           a.gitInfo,
		Config:        cfg,
		AIApiKeyValue: secretOrBlank(cfg.AIAnalysis.APIKey),
	}
	a.render(w, "ai_config.html", data)
}

func (a *App) handleTranslationConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _ := LoadConfig()
	data := TranslationConfigPageData{
		Title:                     "翻译设置",
		ActivePage:                "translation",
		Git:                       a.gitInfo,
		Config:                    cfg,
		TranslationAPIKeyValue:    secretOrBlank(cfg.Translation.APIKey),
		TranslationSecretKeyValue: secretOrBlank(cfg.Translation.SecretKey),
	}
	if r.URL.Query().Get("saved") != "" {
		data.Message = "翻译设置已保存。"
	}
	a.render(w, "translation_config.html", data)
}

func (a *App) handleSaveTranslationConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	cfg, _ := LoadConfig()
	cfg.Translation.Enabled = r.FormValue("translation_enabled") != ""
	cfg.Translation.APIURL = strings.TrimSpace(r.FormValue("translation_api_url"))
	apiKey := strings.TrimSpace(r.FormValue("translation_api_key"))
	if apiKey != "" {
		if apiKey == maskSecret(cfg.Translation.APIKey) {
			apiKey = cfg.Translation.APIKey
		}
	}
	cfg.Translation.APIKey = apiKey
	secretKey := strings.TrimSpace(r.FormValue("translation_secret_key"))
	if secretKey != "" {
		if secretKey == maskSecret(cfg.Translation.SecretKey) {
			secretKey = cfg.Translation.SecretKey
		}
	}
	cfg.Translation.SecretKey = secretKey
	cfg.Translation.Region = strings.TrimSpace(r.FormValue("translation_region"))
	cfg.Translation.Model = strings.TrimSpace(r.FormValue("translation_model"))
	cfg.Translation.SourceLanguage = strings.TrimSpace(r.FormValue("translation_source_language"))
	cfg.Translation.TargetLanguage = strings.TrimSpace(r.FormValue("translation_target_language"))
	if timeoutSeconds, err := strconv.Atoi(strings.TrimSpace(r.FormValue("translation_timeout_seconds"))); err == nil {
		cfg.Translation.TimeoutSeconds = timeoutSeconds
	}
	cfg.Translation.Prompt = strings.TrimSpace(r.FormValue("translation_prompt"))

	if err := SaveConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("translation config saved")
	if cfg.Translation.Enabled {
		go backfillStoredTranslations(a.store)
	}
	http.Redirect(w, r, "/translation_config?saved=1", http.StatusSeeOther)
}

func (a *App) handleSaveAIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	cfg, _ := LoadConfig()
	cfg.AIAnalysis.Enabled = r.FormValue("ai_enabled") == "on"
	cfg.AIAnalysis.APIKey = strings.TrimSpace(r.FormValue("ai_api_key"))
	cfg.AIAnalysis.Prompt = strings.TrimSpace(r.FormValue("ai_prompt"))
	if err := SaveConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("ai config saved, enabled=%t", cfg.AIAnalysis.Enabled)
	http.Redirect(w, r, "/ai_config", http.StatusSeeOther)
}

func (a *App) handleMessagePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, _ := LoadConfig()
	data := MessagePushPageData{
		Title:      "消息推送配置",
		ActivePage: "message",
		Git:        a.gitInfo,
		BotToken:   maskSecret(cfg.Telegram.BotToken),
		ChatID:     maskSecret(cfg.Telegram.ChatID),
	}
	a.render(w, "message_push.html", data)
}

func (a *App) handleMessagePushSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "表单解析失败。"})
		return
	}

	botToken := strings.TrimSpace(r.FormValue("bot_token"))
	chatID := strings.TrimSpace(r.FormValue("chat_id"))
	if botToken == "" || chatID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "Bot Token 和 Chat ID 不能为空。"})
		return
	}

	cfg, _ := LoadConfig()
	if botToken == maskSecret(cfg.Telegram.BotToken) {
		botToken = cfg.Telegram.BotToken
	}
	if chatID == maskSecret(cfg.Telegram.ChatID) {
		chatID = cfg.Telegram.ChatID
	}
	cfg.Telegram.BotToken = botToken
	cfg.Telegram.ChatID = chatID
	if err := SaveConfig(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "message": err.Error()})
		return
	}
	log.Println("telegram config saved")
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": "Telegram 配置已成功保存。"})
}

func (a *App) handleMessagePushTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg, _ := LoadConfig()
	ok, message := sendTelegramTestMessage(cfg)
	if ok {
		log.Println("telegram test message sent")
		writeJSON(w, http.StatusOK, map[string]string{"status": "success", "message": message})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": message})
}

func (a *App) handleConfigPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) loginSessionBaseURL(r *http.Request) string {
	scheme := "http"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(strings.Split(proto, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:8085"
	}

	return scheme + "://" + host
}

func (a *App) loginSessionViewerURL(r *http.Request, sessionID string) string {
	return a.loginSessionBaseURL(r) + "/truthsocial/login/session/" + url.PathEscape(sessionID)
}

func (a *App) loginSessionStatusURL(r *http.Request, sessionID string) string {
	return a.loginSessionBaseURL(r) + "/truthsocial/login/session/" + url.PathEscape(sessionID) + "/status"
}

func (a *App) loginSessionWebSocketURL(r *http.Request, sessionID string) string {
	scheme := "ws"
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") || r.TLS != nil {
		scheme = "wss"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:8085"
	}
	return scheme + "://" + host + "/truthsocial/login/session/" + url.PathEscape(sessionID) + "/vnc"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func splitLines(value string) []string {
	lines := strings.Split(value, "\n")
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, line)
	}
	return items
}

func secretOrBlank(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "YOUR_") {
		return ""
	}
	return value
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "YOUR_") {
		return ""
	}
	if len(value) <= 8 {
		return value
	}
	return value[:4] + strings.Repeat("*", 10) + value[len(value)-4:]
}

func parsePageNumber(value string) int {
	page, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func totalPagesFor(total, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}

func contentBaseURL(selected string) string {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return "/content"
	}
	return "/content/" + url.PathEscape(selected)
}

func buildPaginationLinks(baseURL string, currentPage, totalPages int) []PaginationLink {
	if totalPages <= 1 {
		return nil
	}

	links := make([]PaginationLink, 0, 8)
	addLink := func(label string, page int, active, disabled bool) {
		link := PaginationLink{Label: label, Active: active, Disabled: disabled}
		if !disabled && !active {
			link.URL = fmt.Sprintf("%s?page=%d", baseURL, page)
		}
		links = append(links, link)
	}

	addLink("上一页", currentPage-1, false, currentPage <= 1)

	candidates := []int{
		1,
		totalPages,
		currentPage,
		currentPage - 1,
		currentPage + 1,
		currentPage - 2,
		currentPage + 2,
	}
	seen := map[int]struct{}{}
	pageNumbers := make([]int, 0, len(candidates))
	for _, page := range candidates {
		if page < 1 || page > totalPages {
			continue
		}
		if _, ok := seen[page]; ok {
			continue
		}
		seen[page] = struct{}{}
		pageNumbers = append(pageNumbers, page)
	}
	sort.Ints(pageNumbers)

	last := 0
	for _, page := range pageNumbers {
		if last != 0 && page-last > 1 {
			links = append(links, PaginationLink{Label: "…", Disabled: true})
		}
		addLink(strconv.Itoa(page), page, page == currentPage, false)
		last = page
	}

	addLink("下一页", currentPage+1, false, currentPage >= totalPages)
	return links
}

func getGitCommitInfo() GitInfo {
	info := GitInfo{Time: "N/A", Hash: "N/A", Branch: "N/A"}

	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	if value := run("log", "-1", "--format=%cd", "--date=format:%Y-%m-%d %H:%M:%S"); value != "" {
		info.Time = value
	}
	if value := run("rev-parse", "--short", "HEAD"); value != "" {
		info.Hash = value
	}
	if value := run("rev-parse", "--abbrev-ref", "HEAD"); value != "" {
		info.Branch = value
	}
	return info
}
