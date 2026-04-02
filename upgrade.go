package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const upgradeTransientUnitName = "truthsocial-upgrade"

func (a *App) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	scriptPath, err := resolveUpgradeScriptPath()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	if err := launchUpgradeJob(scriptPath); err != nil {
		log.Printf("upgrade launch failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	log.Printf("upgrade job started: %s", scriptPath)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "success",
		"message": "升级已启动，系统服务将自动拉取、构建并重启。",
	})
}

func launchUpgradeJob(scriptPath string) error {
	systemdRun, err := exec.LookPath("systemd-run")
	if err != nil {
		return fmt.Errorf("systemd-run not found: %w", err)
	}

	workDir := filepath.Dir(scriptPath)
	cmd := exec.Command(
		systemdRun,
		"--no-block",
		"--unit="+upgradeTransientUnitName,
		"--property=Type=oneshot",
		"--property=WorkingDirectory="+workDir,
		"/bin/bash",
		scriptPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	return cmd.Run()
}

func resolveUpgradeScriptPath() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "upgrade.sh"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "upgrade.sh"))
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		key := filepath.Clean(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("upgrade.sh not found")
}
