package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

func loadConfig(path string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("resolving symlinks for %s: %w", path, err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s (resolved: %s): %w", path, resolved, err)
	}

	// Validate it's valid JSON.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return data, nil
}

func hashBytes(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func prettyJSON(data []byte) string {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return string(data)
	}
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.json"
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		log.Fatalf("resolving config path: %v", err)
	}

	// Initial load.
	data, err := loadConfig(absPath)
	if err != nil {
		log.Fatalf("initial config load failed: %v", err)
	}

	currentHash := hashBytes(data)
	log.Printf("config loaded from %s", absPath)
	log.Printf("hash: %s", currentHash)
	log.Printf("contents:\n%s", prettyJSON(data))

	// Set up fsnotify watcher — watch the directory, not the file.
	// This mirrors the pattern from workbench/internal/org_manager/manager.go:Watch
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("creating fsnotify watcher: %v", err)
	}
	defer watcher.Close()

	dir := filepath.Dir(absPath)
	if err := watcher.Add(dir); err != nil {
		log.Fatalf("watching directory %s: %v", dir, err)
	}

	base := filepath.Base(absPath)
	log.Printf("watching for changes in %s (file: %s)", dir, base)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var debounce *time.Timer
	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			if debounce != nil {
				debounce.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				log.Println("watcher events channel closed")
				return
			}

			name := filepath.Base(event.Name)
			isTarget := name == base
			isK8sSwap := name == "..data" && event.Has(fsnotify.Create)

			if !isTarget && !isK8sSwap {
				continue
			}

			log.Printf("change detected: file=%s op=%s", name, event.Op)

			if debounce != nil {
				debounce.Stop()
				log.Println("debounce timer reset")
			}

			debounce = time.AfterFunc(500*time.Millisecond, func() {
				log.Println("debounce fired, reloading config...")

				newData, err := loadConfig(absPath)
				if err != nil {
					log.Printf("reload failed: %v", err)
					return
				}

				newHash := hashBytes(newData)
				if newHash != currentHash {
					log.Printf("config changed: %s -> %s", currentHash, newHash)
					log.Printf("new contents:\n%s", prettyJSON(newData))
					currentHash = newHash
				} else {
					log.Println("config reloaded but hash unchanged")
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				log.Println("watcher errors channel closed")
				return
			}
			log.Printf("fsnotify error: %v", err)
		}
	}
}
