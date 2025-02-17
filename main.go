package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Config struct {
	User       string `json:"user"`
	Pass       string `json:"pass"`
	Ddns       string `json:"ddns"`
	Interval   int    `json:"interval"`    // default 300
	HealthPort int    `json:"health_port"` // default 8080
}

type HealthStatus struct {
	sync.RWMutex
	Healthy    bool      `json:"healthy"`
	LastUpdate time.Time `json:"last_update"`
	LastError  string    `json:"last_error"`
	CurrentIP  string    `json:"current_ip"`
	StartTime  time.Time `json:"-"`
	Interval   int       `json:"interval"`
}

var (
	config             Config
	configPath         = "config/config.json"
	health             = HealthStatus{StartTime: time.Now()}
	ipCache            string
	client             = &http.Client{Timeout: 10 * time.Second}
	ipCheckerCancel    context.CancelFunc
	healthServerCancel context.CancelFunc
)

func main() {
	loadConfig(true)
	go watchConfig()
	startHealthServer()
	startIPChecker()
	select {}
}

func loadConfig(firstLoad bool) {
	file, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	newConfig := Config{Interval: 300, HealthPort: 8080}
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
	health.Lock()
	health.Interval = config.Interval
	health.Unlock()

	log.Printf("Config loaded: %+v", config)
}

func handleConfigChanges(newConfig Config) {
	if newConfig.Interval != config.Interval {
		log.Println("Interval changed, restarting IP checker")
		stopIPChecker()
		startIPChecker()
	}

	if newConfig.HealthPort != config.HealthPort {
		log.Println("Health port changed, restarting health server")
		stopHealthServer()
		startHealthServer()
	}
}

func watchConfig() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	err = watcher.Add(configPath)
	if err != nil {
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

func startHealthServer() {
	ctx, cancel := context.WithCancel(context.Background())
	healthServerCancel = cancel
	go runHealthServer(ctx)
}

func stopHealthServer() {
	if healthServerCancel != nil {
		healthServerCancel()
	}
}

func runHealthServer(ctx context.Context) {
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.HealthPort),
		Handler: http.HandlerFunc(healthHandler),
	}

	go func() {
		log.Printf("Starting health server on :%d", config.HealthPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Health server failed: %v", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Health server shutdown error: %v", err)
	}
}

func checkAndUpdateIP() {
	ip, err := getPublicIP()
	if err != nil {
		health.Lock()
		health.Healthy = false
		health.LastError = err.Error()
		health.Unlock()
		log.Printf("Error getting IP: %v", err)
		return
	}

	health.Lock()
	health.CurrentIP = ip
	health.Unlock()

	if ip != ipCache {
		if err := updateDDNS(ip); err != nil {
			health.Lock()
			health.Healthy = false
			health.LastError = err.Error()
			health.Unlock()
			log.Printf("DDNS update failed: %v", err)
			return
		}
		ipCache = ip
		log.Printf("DDNS updated successfully with IP: %s", ip)
	} else {
		log.Printf("IP unchanged: %s", ip)
	}

	health.Lock()
	health.Healthy = true
	health.LastError = ""
	health.LastUpdate = time.Now()
	health.Unlock()
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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	health.RLock()
	defer health.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Healthy    bool      `json:"healthy"`
		LastUpdate time.Time `json:"last_update"`
		LastError  string    `json:"last_error"`
		CurrentIP  string    `json:"current_ip"`
		Uptime     string    `json:"uptime"`
		Interval   int       `json:"interval"`
	}{
		Healthy:    health.Healthy,
		LastUpdate: health.LastUpdate,
		LastError:  health.LastError,
		CurrentIP:  health.CurrentIP,
		Uptime:     time.Since(health.StartTime).String(),
		Interval:   health.Interval,
	})
}
