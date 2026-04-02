package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const upgradeTransientUnitName = "truthsocial-upgrade"
const upgradeTransientServiceName = upgradeTransientUnitName + ".service"
const upgradeServiceName = "truthsocial-upgrade.service"
const truthsocialServiceName = "truthsocial.service"
const upgradeLogFileName = "upgrade.log"

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

	baseDir, err := resolveUpgradeBaseDir()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	logPath := filepath.Join(baseDir, upgradeLogFileName)
	startMarker := fmt.Sprintf("[%s] upgrade requested from web UI\n", time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(logPath, []byte(startMarker), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	if err := launchUpgradeJob(scriptPath); err != nil {
		_ = appendUpgradeLogLine(baseDir, fmt.Sprintf("[%s] upgrade failed to launch: %v\n", time.Now().UTC().Format(time.RFC3339), err))
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

func (a *App) handleUpgradeLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	baseDir, err := resolveUpgradeBaseDir()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	logPath := filepath.Join(baseDir, upgradeLogFileName)
	logText, readErr := readTailFile(logPath, 128*1024)
	if readErr != nil && !os.IsNotExist(readErr) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"status":  "error",
			"message": readErr.Error(),
		})
		return
	}

	running := upgradeJobRunning()
	serverStatus := readTruthSocialServiceStatus()
	serverLog := readLatestLogLines(filepath.Join(baseDir, "go.log"), 20)
	status := "idle"
	if running {
		status = "running"
	} else if strings.Contains(logText, "upgrade failed") {
		status = "failed"
	} else if strings.Contains(logText, "upgrade finished") {
		status = "finished"
	} else if strings.TrimSpace(logText) != "" {
		status = "running"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        status,
		"running":       running,
		"finished":      !running && (strings.Contains(logText, "upgrade failed") || strings.Contains(logText, "upgrade finished")),
		"log":           logText,
		"server_status": serverStatus,
		"server_log":    serverLog,
	})
}

func launchUpgradeJob(scriptPath string) error {
	if upgradeServiceAvailable() {
		return launchUpgradeViaSystemctl(scriptPath)
	}

	systemdRun, err := resolveSystemdRunPath()
	if err == nil {
		return launchUpgradeViaSystemdRun(systemdRun, scriptPath)
	}

	return launchUpgradeViaShell(scriptPath)
}

func launchUpgradeViaSystemctl(scriptPath string) error {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("未找到 systemctl，请确认服务器已安装 systemd")
	}

	workDir := filepath.Dir(scriptPath)
	cmd := exec.Command(systemctl, "start", "--no-block", upgradeServiceName)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return fmt.Errorf("启动升级服务失败: %w: %s", err, text)
		}
		return fmt.Errorf("启动升级服务失败: %w", err)
	}
	return nil
}

func launchUpgradeViaSystemdRun(systemdRun, scriptPath string) error {
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
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return fmt.Errorf("systemd-run 启动升级任务失败: %w: %s", err, text)
		}
		return fmt.Errorf("systemd-run 启动升级任务失败: %w", err)
	}
	return nil
}

func resolveSystemdRunPath() (string, error) {
	candidates := []string{
		"systemd-run",
		"/usr/bin/systemd-run",
		"/bin/systemd-run",
		"/usr/sbin/systemd-run",
	}
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 systemd-run，请确认服务器已安装 systemd")
}

func launchUpgradeViaShell(scriptPath string) error {
	workDir := filepath.Dir(scriptPath)
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动升级脚本失败: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func resolveUpgradeScriptPath() (string, error) {
	baseDir, err := resolveUpgradeBaseDir()
	if err != nil {
		return "", err
	}
	scriptPath := filepath.Join(baseDir, "upgrade.sh")
	if _, err := os.Stat(scriptPath); err == nil {
		return scriptPath, nil
	}
	return "", fmt.Errorf("upgrade.sh not found")
}

func resolveUpgradeBaseDir() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(candidate, "upgrade.sh")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("upgrade.sh not found")
}

func upgradeJobRunning() bool {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	if state := unitActiveState(systemctl, upgradeServiceName); state == "active" || state == "activating" {
		return true
	}
	if state := unitActiveState(systemctl, upgradeTransientServiceName); state == "active" || state == "activating" {
		return true
	}
	return false
}

func upgradeServiceAvailable() bool {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	out, err := exec.Command(systemctl, "show", upgradeServiceName, "-p", "LoadState").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "LoadState=loaded")
}

func unitActiveState(systemctl, unit string) string {
	out, err := exec.Command(systemctl, "is-active", unit).CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out))
}

func readTailFile(path string, maxBytes int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}

	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if size := info.Size(); size > maxBytes {
		if _, err := file.Seek(size-maxBytes, io.SeekStart); err != nil {
			return "", err
		}
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func appendUpgradeLogLine(baseDir, line string) error {
	logPath := filepath.Join(baseDir, upgradeLogFileName)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, err = file.WriteString(line)
	return err
}

func readLatestLogLines(path string, lines int) string {
	text, err := readTailFile(path, 256*1024)
	if err != nil {
		return ""
	}
	if lines <= 0 {
		lines = 20
	}
	parts := strings.Split(text, "\n")
	if len(parts) == 0 {
		return ""
	}

	start := 0
	if len(parts) > lines {
		start = len(parts) - lines
	}
	selected := make([]string, 0, len(parts)-start)
	for _, line := range parts[start:] {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		selected = append(selected, line)
	}
	return strings.Join(selected, "\n")
}

func readTruthSocialServiceStatus() string {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return "服务器状态: 未找到 systemctl"
	}

	args := []string{"show", truthsocialServiceName, "-p", "ActiveState", "-p", "SubState", "-p", "MainPID", "-p", "UnitFileState"}
	out, err := exec.Command(systemctl, args...).CombinedOutput()
	if err != nil {
		state := strings.TrimSpace(string(out))
		if state == "" {
			return "服务器状态: 无法获取"
		}
		return "服务器状态: " + state
	}

	fields := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		fields[parts[0]] = parts[1]
	}

	activeState := strings.TrimSpace(fields["ActiveState"])
	subState := strings.TrimSpace(fields["SubState"])
	mainPID := strings.TrimSpace(fields["MainPID"])
	unitState := strings.TrimSpace(fields["UnitFileState"])

	if activeState == "" {
		activeState = "unknown"
	}

	var b strings.Builder
	b.WriteString("服务器状态: ")
	b.WriteString(activeState)
	if subState != "" {
		b.WriteString(" (")
		b.WriteString(subState)
		b.WriteString(")")
	}
	if mainPID != "" && mainPID != "0" {
		b.WriteString(", PID=")
		b.WriteString(mainPID)
	}
	if unitState != "" {
		b.WriteString(", unit=")
		b.WriteString(unitState)
	}
	return b.String()
}
