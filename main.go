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
	config           Config
	configPath       = "config/config.json"
	ipCache          string
	client           = &http.Client{Timeout: 10 * time.Second}
	ipCheckerCancel  context.CancelFunc
	ipCheckerRunning bool
)

func main() {
	if loadConfig(true) {
		startIPChecker()
	}
	go watchConfig()
	select {}
}

func loadConfig(firstLoad bool) bool {
	file, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("Error reading config: %v. Waiting for valid config...", err)
		return false
	}

	newConfig := Config{Interval: 300}
	if err := json.Unmarshal(file, &newConfig); err != nil {
		log.Printf("Error parsing config: %v. Waiting for valid config...", err)
		return false
	}

	if newConfig.Interval < 60 {
		newConfig.Interval = 300
	}

	if !isValidConfig(newConfig) {
		log.Printf("Invalid config: user, pass, or ddns is missing. Waiting for valid config...")
		return false
	}

	if !firstLoad {
		handleConfigChanges(newConfig)
	} else if !isValidConfig(config) && reflect.DeepEqual(config, Config{}) {
		// Initial load with valid config
		config = newConfig
		log.Println("Config loaded successfully")
		return true
	}

	if !reflect.DeepEqual(newConfig, config) {
		config = newConfig
		log.Println("Config loaded successfully")
	}
	return true
}

func isValidConfig(c Config) bool {
	return c.User != "" && c.Pass != "" && c.Ddns != ""
	// || c.User != "your_username" && c.Pass != "your_password" && c.Ddns != "your.ddns.provider"
}

func handleConfigChanges(newConfig Config) {
	if !reflect.DeepEqual(newConfig, config) {
		log.Println("Config changed, restarting IP checker")
		stopIPChecker()
		startIPChecker()
	}
}

func watchConfig() {
	for {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Printf("Failed to create watcher: %v. Retrying in 10 seconds...", err)
			time.Sleep(10 * time.Second)
			continue
		}
		defer watcher.Close()

		err = watcher.Add(configPath)
		if err != nil {
			log.Printf("Failed to watch config: %v. Retrying in 10 seconds...", err)
			time.Sleep(10 * time.Second)
			continue
		}

		log.Println("Watching config file for changes...")
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
}

func startIPChecker() {
	if ipCheckerRunning {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	ipCheckerCancel = cancel
	ipCheckerRunning = true
	go runIPChecker(ctx)
}

func stopIPChecker() {
	if ipCheckerCancel != nil {
		ipCheckerCancel()
	}
	ipCheckerRunning = false
}

func runIPChecker(ctx context.Context) {
	defer func() { ipCheckerRunning = false }()

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
		return
	}

	if ip == ipCache {
		log.Printf("IP unchanged: %s", ip)
		return
	}

	if err := updateDDNS(ip); err != nil {
		log.Printf("DDNS update failed: %v", err)
		log.Println("Please check your credentials and ddns provider URL in the config file")
		return
	}

	ipCache = ip
	log.Printf("DDNS updated successfully with IP: %s", ip)
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
