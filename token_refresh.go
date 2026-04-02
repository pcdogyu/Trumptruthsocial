package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func ensureBearerTokenOnStartup() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("load config before token refresh failed: %v", err)
		return
	}

	if !bearerTokenNeedsRefresh(cfg, time.Now().UTC()) {
		return
	}

	log.Printf("Bearer Token 已超过有效期或缺失，正在自动抓取新的 Token...")
	if err := runBearerTokenRefreshScript(); err != nil {
		log.Printf("自动抓取 Bearer Token 失败: %v", err)
		return
	}

	if updatedCfg, err := LoadConfig(); err == nil {
		log.Printf("Bearer Token 已刷新完成，当前有效期为 %d 天。", validBearerTokenValidityDays(updatedCfg.Auth.BearerTokenValidityDays))
	} else {
		log.Printf("Bearer Token 已刷新完成，但重新加载配置失败: %v", err)
	}
}

func runBearerTokenRefreshScript() error {
	scriptPath := resolveTokenScriptPath()
	if scriptPath == "" {
		return fmt.Errorf("get_token.py not found")
	}

	commands := [][]string{
		{"python", scriptPath},
		{"py", "-3", scriptPath},
		{"python3", scriptPath},
	}
	var lastErr error
	for _, args := range commands {
		if err := runCommand(args[0], args[1:]...); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func resolveTokenScriptPath() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "get_token.py"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "get_token.py"))
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[strings.ToLower(candidate)]; ok {
			continue
		}
		seen[strings.ToLower(candidate)] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
