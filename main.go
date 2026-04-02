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
	if len(os.Args) > 1 && os.Args[1] == "get-token" {
		os.Exit(runTokenGrabber(os.Args[2:]))
	}

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
	debugf("logging target configured: console=%t file=%t debug=%t", true, err == nil, debugEnabled)

	if err := ensureFiles(); err != nil {
		log.Fatalf("failed to initialize config: %v", err)
	}

	ensureBearerTokenOnStartup()

	store, err := NewPostStore()
	if err != nil {
		log.Fatalf("failed to initialize store: %v", err)
	}

	app, err := newApp(store)
	if err != nil {
		log.Fatalf("failed to initialize app: %v", err)
	}

	go runMonitor(store)

	listenAddr := "0.0.0.0:8085"
	log.Printf("启动 Web UI，请访问: http://%s", listenAddr)
	if err := http.ListenAndServe(listenAddr, app.routes()); err != nil {
		log.Fatalf("http server failed: %v", err)
	}
}
