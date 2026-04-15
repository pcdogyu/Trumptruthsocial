package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// urlRe 匹配完整 URL（http/https），翻译前从正文中剥离以节省 token。
var urlRe = regexp.MustCompile(`https?://\S+`)

const defaultTranslationPrompt = "Translate the following text into the target language, preserving meaning and tone:"

type translationAPIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type translationAPIRequest struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []translationAPIMessage `json:"messages"`
	Temperature float64                 `json:"temperature,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
}

func enrichPostTranslation(cfg Config, post *Post, allowRetry bool) {
	if post == nil {
		return
	}
	if !cfg.Translation.Enabled {
		return
	}
	if strings.TrimSpace(post.Content) == "" {
		return
	}
	if strings.TrimSpace(post.TranslatedContent) != "" && strings.TrimSpace(post.TranslationError) == "" {
		return
	}
	// 已处理过但内容仅含URL（TranslatedAt已设置、内容为空、无错误），跳过重试
	if strings.TrimSpace(post.TranslatedAt) != "" && strings.TrimSpace(post.TranslatedContent) == "" && strings.TrimSpace(post.TranslationError) == "" {
		return
	}
	if !allowRetry && strings.TrimSpace(post.TranslationError) != "" {
		return
	}

	translated, err := translateText(cfg, post.Content)
	if err != nil {
		post.TranslationError = err.Error()
		if strings.TrimSpace(post.TranslatedContent) == "" {
			post.TranslatedContent = ""
		}
		return
	}
	post.TranslatedAt = time.Now().UTC().Format(time.RFC3339)
	post.TranslatedContent = strings.TrimSpace(translated)
	post.TranslationError = ""
}

func hydratePostTranslationFromStore(store *PostStore, post *Post) {
	if store == nil || post == nil {
		return
	}
	if stored, ok := store.GetPostByID(post.ID); ok {
		post.TranslatedContent = stored.TranslatedContent
		post.TranslatedAt = stored.TranslatedAt
		post.TranslationError = stored.TranslationError
	}
}

// stripURLsForTranslation 去除正文中的 URL，保留 URL 后的非链接文字。
// 例：" check https://t.co/abc/xyz out" → " check  out"
func stripURLsForTranslation(text string) string {
	return strings.TrimSpace(urlRe.ReplaceAllString(text, ""))
}

func translateText(cfg Config, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", nil
	}
	content = stripURLsForTranslation(content)
	if content == "" {
		return "", nil
	}
	if !cfg.Translation.Enabled {
		return "", errors.New("翻译功能未启用")
	}

	apiURL := strings.TrimSpace(cfg.Translation.APIURL)
	if apiURL == "" || strings.Contains(apiURL, "YOUR_TRANSLATION_API_URL") {
		return "", errors.New("翻译 API URL 未配置")
	}

	// 腾讯云 TMT：SecretKey 不为空时使用 TC3-HMAC-SHA256 签名认证
	secretKey := strings.TrimSpace(cfg.Translation.SecretKey)
	if secretKey != "" && !strings.Contains(secretKey, "YOUR_") {
		return translateTextTencent(cfg, content)
	}

	requestText := buildTranslationRequestText(cfg, content)
	if requestText == "" {
		return "", errors.New("翻译请求内容为空")
	}

	payload := translationAPIRequest{
		Messages: []translationAPIMessage{
			{
				Role:    "user",
				Content: requestText,
			},
		},
		Temperature: 0,
	}

	model := strings.TrimSpace(cfg.Translation.Model)
	if model != "" && !strings.Contains(model, "YOUR_TRANSLATION_MODEL") {
		payload.Model = model
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	timeout := cfg.Translation.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	apiKey := strings.TrimSpace(cfg.Translation.APIKey)
	if apiKey != "" && !strings.Contains(apiKey, "YOUR_TRANSLATION_API_KEY") {
		bearerToken := strings.TrimSpace(strings.TrimPrefix(apiKey, "Bearer "))
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
			req.Header.Set("X-API-Key", bearerToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("翻译 API 请求失败: %s", translationHTTPErrorMessage(resp.Status, respBody))
	}

	text, err := extractTranslationText(respBody)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("翻译 API 返回空内容")
	}
	return text, nil
}

func buildTranslationRequestText(cfg Config, content string) string {
	prompt := strings.TrimSpace(cfg.Translation.Prompt)
	if prompt == "" || strings.Contains(prompt, "YOUR_TRANSLATION") {
		prompt = defaultTranslationPrompt
	}

	replacements := map[string]string{
		"{source_language}": strings.TrimSpace(cfg.Translation.SourceLanguage),
		"{target_language}": strings.TrimSpace(cfg.Translation.TargetLanguage),
		"{content}":         content,
	}

	hasContentPlaceholder := strings.Contains(prompt, "{content}")
	for key, value := range replacements {
		prompt = strings.ReplaceAll(prompt, key, value)
	}
	if hasContentPlaceholder {
		return strings.TrimSpace(prompt)
	}

	sourceLanguage := strings.TrimSpace(cfg.Translation.SourceLanguage)
	targetLanguage := strings.TrimSpace(cfg.Translation.TargetLanguage)
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n")
	if sourceLanguage != "" {
		b.WriteString("Source language: ")
		b.WriteString(sourceLanguage)
		b.WriteString("\n")
	}
	if targetLanguage != "" {
		b.WriteString("Target language: ")
		b.WriteString(targetLanguage)
		b.WriteString("\n")
	}
	b.WriteString("Content:\n")
	b.WriteString(content)
	return strings.TrimSpace(b.String())
}

func translationHTTPErrorMessage(status string, body []byte) string {
	if len(body) == 0 {
		return "翻译 API 请求失败: " + status
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Sprintf("翻译 API 请求失败: %s, 返回: %s", status, strings.TrimSpace(string(body)))
	}

	for _, key := range []string{"error", "message", "detail", "description"} {
		if value := extractStringFromAny(payload[key]); value != "" {
			return fmt.Sprintf("翻译 API 请求失败: %s, 详情: %s", status, value)
		}
	}

	if text := extractTranslationTextFromAny(payload); text != "" {
		return fmt.Sprintf("翻译 API 请求失败: %s, 返回: %s", status, text)
	}

	return "翻译 API 请求失败: " + status
}

func extractTranslationText(body []byte) (string, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	text := extractTranslationTextFromAny(payload)
	if text == "" {
		return "", errors.New("翻译 API 未返回可识别的译文")
	}
	return text, nil
}

func extractTranslationTextFromAny(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if text := extractTranslationTextFromMap(v); text != "" {
			return text
		}
		for _, key := range []string{"data", "result", "response"} {
			if text := extractTranslationTextFromAny(v[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range v {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	case string:
		return strings.TrimSpace(v)
	}
	return ""
}

func extractTranslationTextFromMap(payload map[string]any) string {
	if text := extractStringFromAny(payload["output_text"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["translated_text"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["translation"]); text != "" {
		return text
	}
	if text := extractStringFromAny(payload["text"]); text != "" {
		return text
	}

	if choices, ok := payload["choices"].([]any); ok {
		for _, choice := range choices {
			if text := extractTranslationTextFromAny(choice); text != "" {
				return text
			}
		}
	}
	if data, ok := payload["data"].([]any); ok {
		for _, item := range data {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	}

	if nested, ok := payload["message"].(map[string]any); ok {
		if text := extractStringFromAny(nested["content"]); text != "" {
			return text
		}
	}
	if nested, ok := payload["content"].([]any); ok {
		for _, item := range nested {
			if text := extractTranslationTextFromAny(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractStringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"message", "detail", "description", "error", "msg"} {
			if text := extractStringFromAny(v[key]); text != "" {
				return text
			}
		}
		if text := extractStringFromAny(v["content"]); text != "" {
			return text
		}
		if text := extractStringFromAny(v["text"]); text != "" {
			return text
		}
	case []any:
		for _, item := range v {
			if text := extractStringFromAny(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func backfillStoredTranslations(store *PostStore) {
	if store == nil {
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		return
	}
	if !cfg.Translation.Enabled {
		return
	}

	posts := store.GetAllPosts("", 0, 0)
	if len(posts) == 0 {
		return
	}

	for _, post := range posts {
		if strings.TrimSpace(post.Content) == "" {
			continue
		}
		if strings.TrimSpace(post.TranslatedContent) != "" && strings.TrimSpace(post.TranslationError) == "" {
			continue
		}
		enrichPostTranslation(cfg, &post, true)
		if _, err := store.UpdatePostTranslation(post.ID, post.TranslatedContent, post.TranslatedAt, post.TranslationError); err != nil {
			continue
		}
	}
}

// ── 腾讯云机器翻译（TMT）TC3-HMAC-SHA256 实现 ──────────────────────────────

func tc3HmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func tc3SHA256Hex(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// translateTextTencent 使用腾讯云 TMT TextTranslate 接口翻译文本。
// 认证方式：TC3-HMAC-SHA256，SecretId 存于 cfg.Translation.APIKey，
// SecretKey 存于 cfg.Translation.SecretKey。
func translateTextTencent(cfg Config, content string) (string, error) {
	apiURL := strings.TrimSpace(cfg.Translation.APIURL)
	secretID := strings.TrimSpace(cfg.Translation.APIKey)
	secretKey := strings.TrimSpace(cfg.Translation.SecretKey)

	if secretID == "" || strings.Contains(secretID, "YOUR_") {
		return "", errors.New("腾讯云 SecretId（翻译 API Key）未配置")
	}

	source := strings.TrimSpace(cfg.Translation.SourceLanguage)
	if source == "" {
		source = "auto"
	}
	target := strings.TrimSpace(cfg.Translation.TargetLanguage)
	if target == "" {
		target = "zh"
	}

	type tcRequest struct {
		SourceText string `json:"SourceText"`
		Source     string `json:"Source"`
		Target     string `json:"Target"`
		ProjectId  int    `json:"ProjectId"`
	}
	body, err := json.Marshal(tcRequest{SourceText: content, Source: source, Target: target, ProjectId: 0})
	if err != nil {
		return "", err
	}

	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("无效的翻译 API URL: %v", err)
	}
	host := u.Hostname()

	// 从主机名中提取服务名（如 "tmt.tencentcloudapi.com" → "tmt"）
	service := "tmt"
	if parts := strings.SplitN(host, ".", 2); len(parts) > 0 && parts[0] != "" {
		service = parts[0]
	}

	now := time.Now().UTC()
	timestamp := now.Unix()
	date := now.Format("2006-01-02")
	contentType := "application/json"

	// Step 1: 构造规范请求
	canonicalHeaders := "content-type:" + contentType + "\nhost:" + host + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := strings.Join([]string{
		"POST", "/", "",
		canonicalHeaders,
		signedHeaders,
		tc3SHA256Hex(string(body)),
	}, "\n")

	// Step 2: 构造待签字符串
	credentialScope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		fmt.Sprintf("%d", timestamp),
		credentialScope,
		tc3SHA256Hex(canonicalRequest),
	}, "\n")

	// Step 3: 计算签名
	secretDate := tc3HmacSHA256([]byte("TC3"+secretKey), date)
	secretSvc := tc3HmacSHA256(secretDate, service)
	secretSign := tc3HmacSHA256(secretSvc, "tc3_request")
	signature := hex.EncodeToString(tc3HmacSHA256(secretSign, stringToSign))

	authorization := fmt.Sprintf(
		"TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		secretID, credentialScope, signedHeaders, signature,
	)

	timeout := cfg.Translation.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", "TextTranslate")
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-TC-Version", "2018-03-21")
	if region := strings.TrimSpace(cfg.Translation.Region); region != "" {
		req.Header.Set("X-TC-Region", region)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tcResp struct {
		Response struct {
			TargetText string `json:"TargetText"`
			Error      *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error,omitempty"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(respBody, &tcResp); err != nil {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return "", fmt.Errorf("解析腾讯云翻译响应失败: %v, 响应: %s", err, snippet)
	}
	if tcResp.Response.Error != nil {
		return "", fmt.Errorf("腾讯云翻译 API 错误 [%s]: %s",
			tcResp.Response.Error.Code, tcResp.Response.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("翻译 API HTTP 错误: %s", resp.Status)
	}

	text := strings.TrimSpace(tcResp.Response.TargetText)
	if text == "" {
		return "", errors.New("腾讯云翻译 API 返回空内容")
	}
	return text, nil
}
