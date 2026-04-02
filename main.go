package main

import (
	"io"
	"log"
	"net/http"
	"os"
)

func ensureFiles() error {
	if _, err := os.Stat(configFileName); os.IsNotExist(err) {
		if err := SaveConfig(defaultConfig()); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	rotator, err := NewDailyRotatingWriter(".", "go.log")
	if err != nil {
		log.Printf("failed to initialize log file: %v", err)
	} else {
		log.SetOutput(io.MultiWriter(os.Stdout, rotator))
		defer func() {
			_ = rotator.Close()
		}()
	}
	log.Println("logging initialized")

	if err := ensureFiles(); err != nil {
		log.Fatalf("failed to initialize config: %v", err)
	}

	store, err := NewPostStore()
	if err != nil {
		log.Fatalf("failed to initialize store: %v", err)
	}

	app, err := newApp(store)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	go runMonitor(store)

	log.Println("启动 Web UI，请访问: http://127.0.0.1:8085")
	if err := http.ListenAndServe("127.0.0.1:8085", app.routes()); err != nil {
		log.Fatalf("http server failed: %v", err)
	}
}
