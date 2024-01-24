package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"

	"github.com/fsnotify/fsnotify"
)

var (
	metrics []byte
	mu      sync.RWMutex
)

const metricsFile = "/path/to/your/metrics/file"

func main() {
	err := loadMetrics()
	if err != nil {
		log.Fatalf("failed to load metrics: %v", err)
	}

	go watchMetrics()

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()

		if _, err := w.Write(metrics); err != nil {
			http.Error(w, "failed to write response", http.StatusInternalServerError)
		}
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func loadMetrics() error {
	data, err := ioutil.ReadFile(metricsFile)
	if err != nil {
		return err
	}

	// Validate the file contents - this is a very basic check
	// You might want to add more advanced validation here
	if len(data) == 0 {
		return fmt.Errorf("metrics file is empty")
	}

	mu.Lock()
	defer mu.Unlock()

	metrics = data

	return nil
}

func watchMetrics() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("failed to create watcher: %v", err)
	}

	err = watcher.Add(metricsFile)
	if err != nil {
		log.Fatalf("failed to watch metrics file: %v", err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println("metrics file modified, reloading...")

				err := loadMetrics()
				if err != nil {
					log.Printf("failed to reload metrics: %v", err)
					continue
				}

				log.Println("metrics file reloaded successfully")
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			log.Printf("watcher error: %v", err)
		}
	}
}
