package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Config struct {
	User     string `json:"user"`
	Pass     string `json:"pass"`
	Ddns     string `json:"ddns"`
	Interval int    `json:"interval"`
}

var (
	config          Config
	configPath      = "config/config.json"
	ipCache         string
	client          = &http.Client{Timeout: 10 * time.Second}
	ipCheckerCancel context.CancelFunc
)

func main() {
	loadConfig(true)
	go watchConfig()
	startIPChecker()
	select {}
}

func loadConfig(firstLoad bool) {
	file, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	newConfig := Config{Interval: 300}
	if err := json.Unmarshal(file, &newConfig); err != nil {
		log.Printf("Error parsing config: %v", err)
		return
	}

	if newConfig.Interval < 60 {
		newConfig.Interval = 300
	}

	if !firstLoad {
		handleConfigChanges(newConfig)
	}

	config = newConfig
	log.Println("Config loaded successfully")
}

func handleConfigChanges(newConfig Config) {
	if reflect.DeepEqual(newConfig, config) {
		log.Println("Config changed, restarting IP checker")
		stopIPChecker()
		startIPChecker()
	}
}

func watchConfig() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("Watcher create error:", err)
		log.Fatal(err)
	}
	defer watcher.Close()

	if err := watcher.Add(configPath); err != nil {
		log.Println("Watcher add config error:", err)
		log.Fatal(err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println("Config file modified")
				loadConfig(false)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("Watcher error:", err)
		}
	}
}

func startIPChecker() {
	ctx, cancel := context.WithCancel(context.Background())
	ipCheckerCancel = cancel
	go runIPChecker(ctx)
}

func stopIPChecker() {
	if ipCheckerCancel != nil {
		ipCheckerCancel()
	}
}

func runIPChecker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
	defer ticker.Stop()

	checkAndUpdateIP()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkAndUpdateIP()
		}
	}
}

func checkAndUpdateIP() {
	ip, err := getPublicIP()
	if err != nil {
		log.Printf("Error getting IP: %v", err)
	} else {
		if ip != ipCache {
			if err := updateDDNS(ip); err != nil {
				log.Printf("DDNS update failed: %v", err)
			} else {
				ipCache = ip
				log.Printf("DDNS updated successfully with IP: %s", ip)
			}
		} else {
			log.Printf("IP unchanged: %s", ip)
		}
	}
}

func getPublicIP() (string, error) {
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %s", resp.Status)
	}

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

func updateDDNS(ip string) error {
	url := fmt.Sprintf("https://%s:%s@%s?myip=%s",
		config.User, config.Pass, config.Ddns, ip)

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ddns update failed with status: %s", resp.Status)
	}

	return nil
}
