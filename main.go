package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Config struct {
	User       string `json:"user"`
	Pass       string `json:"pass"`
	Ddns       string `json:"ddns"`
	Interval   int    `json:"interval"`
	HealthPort int    `json:"health_port"`
}

var (
	config       Config
	configPath   = "config/config.json"
	configLock   sync.RWMutex
	ipCache      string
	healthStatus struct {
		sync.RWMutex
		LastUpdate time.Time
		LastError  string
		IP         string
		IsHealthy  bool
	}
)

// Add startup time tracking
var startTime = time.Now()

func main() {
	// Initial config load
	if err := loadConfig(); err != nil {
		panic(err)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	defer watcher.Close()

	// Watch config file
	if err := watcher.Add(configPath); err != nil {
		panic(err)
	}

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	// Start health check server
	go startHealthServer()

	// Start the main loop
	go runDDNSUpdater(ctx)

	// Main loop
	for {
		select {
		case <-sigChan:
			fmt.Println("Shutting down...")
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				fmt.Println("Config file modified, reloading...")
				if err := loadConfig(); err != nil {
					fmt.Printf("Error reloading config: %v\n", err)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("Watcher error: %v\n", err)
		}
	}
}

func startHealthServer() {
	configLock.RLock()
	port := config.HealthPort
	if port == 0 {
		port = 8080 // default port
	}
	configLock.RUnlock()

	http.HandleFunc("/health", healthHandler)
	fmt.Printf("Starting health check server on :%d\n", port)
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		fmt.Printf("Health server failed: %v\n", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	healthStatus.RLock()
	defer healthStatus.RUnlock()

	status := map[string]interface{}{
		"healthy":     healthStatus.IsHealthy,
		"last_update": healthStatus.LastUpdate,
		"last_error":  healthStatus.LastError,
		"current_ip":  healthStatus.IP,
		"uptime":      time.Since(startTime).String(),
	}

	configLock.RLock()
	status["interval"] = config.Interval
	configLock.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if !healthStatus.IsHealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(status)
}

func updateHealthStatus(healthy bool, errorMsg string, ip string) {
	healthStatus.Lock()
	defer healthStatus.Unlock()

	healthStatus.IsHealthy = healthy
	healthStatus.LastError = errorMsg
	healthStatus.IP = ip
	if healthy {
		healthStatus.LastUpdate = time.Now()
	}
}

func loadConfig() error {
	configLock.Lock()
	defer configLock.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var newConfig Config
	if err := json.Unmarshal(data, &newConfig); err != nil {
		return err
	}

	// Validate interval
	if newConfig.Interval < 1 {
		newConfig.Interval = 300 // default to 5 minutes
	}

	config = newConfig
	return nil
}

func runDDNSUpdater(ctx context.Context) {
	var ticker *time.Ticker
	var currentInterval int

	// Initial setup
	configLock.RLock()
	currentInterval = config.Interval
	ticker = time.NewTicker(time.Duration(currentInterval) * time.Second)
	configLock.RUnlock()

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			updateIPAndDDNS()

			// Check for interval changes
			configLock.RLock()
			newInterval := config.Interval
			configLock.RUnlock()

			if newInterval != currentInterval {
				currentInterval = newInterval
				ticker.Reset(time.Duration(currentInterval) * time.Second)
				fmt.Printf("Update interval changed to %d seconds\n", currentInterval)
			}
		}
	}
}
func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

func updateDDNS(ip string) error {
	configLock.RLock()
	defer configLock.RUnlock()

	client := &http.Client{}
	url := fmt.Sprintf("https://%s:%s@%s?myip=%s", config.User, config.Pass, config.Ddns, ip)
	req, err := http.NewRequest("Get", url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DDNS update failed with status: %s", resp.Status)
	}

	return nil
}

func updateIPAndDDNS() {
	newIP, err := getPublicIP()
	if err != nil {
		updateHealthStatus(false, err.Error(), "")
		fmt.Printf("Error getting public IP: %v\n", err)
		return
	}

	if newIP == ipCache {
		fmt.Println("IP unchanged, skipping update")
		updateHealthStatus(true, "", "")
		return
	}

	fmt.Printf("Detected new IP: %s\n", newIP)

	if err := updateDDNS(newIP); err != nil {
		updateHealthStatus(false, err.Error(), newIP)
		fmt.Printf("Error updating DDNS: %v\n", err)
		return
	}

	ipCache = newIP
	updateHealthStatus(true, "", "")
	fmt.Println("Successfully updated DDNS record")
}
