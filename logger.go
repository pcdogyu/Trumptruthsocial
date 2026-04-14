package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var debugEnabled = isDebugLoggingEnabled()

type DailyRotatingWriter struct {
	mu          sync.Mutex
	dir         string
	baseName    string
	currentDate string
	file        *os.File
}

func NewDailyRotatingWriter(dir, baseName string) (*DailyRotatingWriter, error) {
	w := &DailyRotatingWriter{
		dir:         dir,
		baseName:    baseName,
		currentDate: time.Now().Format("2006-01-02"),
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := w.prepareCurrentFile(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *DailyRotatingWriter) prepareCurrentFile() error {
	path := filepath.Join(w.dir, w.baseName)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		fileDate := info.ModTime().Format("2006-01-02")
		if fileDate != w.currentDate {
			if err := rotateToArchive(path, filepath.Join(w.dir, archiveName(fileDate))); err != nil {
				return err
			}
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = file
	return nil
}

func (w *DailyRotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if w.currentDate != today {
		if err := w.rotateLocked(today); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *DailyRotatingWriter) rotateLocked(newDate string) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	src := filepath.Join(w.dir, w.baseName)
	archive := filepath.Join(w.dir, archiveName(w.currentDate))
	if info, err := os.Stat(src); err == nil && info.Size() > 0 {
		if err := rotateToArchive(src, archive); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(src, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w.file = file
	w.currentDate = newDate
	return nil
}

func (w *DailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func archiveName(date string) string {
	return fmt.Sprintf("go-%s.log.gz", date)
}

func isDebugLoggingEnabled() bool {
	for _, key := range []string{"DEBUG", "DEBUG_LOG", "LOG_LEVEL"} {
		value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
		switch value {
		case "1", "true", "yes", "on", "debug":
			return true
		}
	}
	return false
}

func debugf(format string, args ...any) {
	if !debugEnabled {
		return
	}
	log.Printf("[DEBUG] "+format, args...)
}

func rotateToArchive(srcPath, dstPath string) error {
	archiveTmp := dstPath + ".tmp"
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(archiveTmp)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(out)
	_, copyErr := io.Copy(gz, in)
	closeErr := gz.Close()
	fileCloseErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(archiveTmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(archiveTmp)
		return closeErr
	}
	if fileCloseErr != nil {
		_ = os.Remove(archiveTmp)
		return fileCloseErr
	}

	_ = os.Remove(dstPath)
	if err := os.Rename(archiveTmp, dstPath); err != nil {
		_ = os.Remove(archiveTmp)
		return err
	}
	return os.Remove(srcPath)
}
