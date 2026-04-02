package main

import (
	"log"
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
	if code := runTokenGrabber(nil); code != 0 {
		log.Printf("自动抓取 Bearer Token 失败，退出码: %d", code)
		return
	}

	if updatedCfg, err := LoadConfig(); err == nil {
		log.Printf("Bearer Token 已刷新完成，当前有效期为 %d 天。", validBearerTokenValidityDays(updatedCfg.Auth.BearerTokenValidityDays))
	} else {
		log.Printf("Bearer Token 已刷新完成，但重新加载配置失败: %v", err)
	}
}
