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
	Interval   int    `json:"interval"`
	HealthPort int    `json:"health_port"`
}

type HealthStatus struct {
	sync.RWMutex
	StartTime     time.Time        `json:"-"`
	CurrentIP     string           `json:"-"`
	FailingStreak int              `json:"FailingStreak"`
	Log           []HealthLogEntry `json:"Log"`
	Status        string           `json:"Status"`
	LastError     string           `json:"-"`
}

type HealthLogEntry struct {
	Start    time.Time `json:"Start"`
	End      time.Time `json:"End"`
	ExitCode int       `json:"ExitCode"`
	Output   string    `json:"Output"`
}

var (
	config             Config
	configPath         = "config/config.json"
	health             = HealthStatus{StartTime: time.Now(), Status: "starting"}
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
	log.Println("Config loaded successfully")
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
	start := time.Now()
	ip, err := getPublicIP()
	end := time.Now()

	var entry HealthLogEntry
	health.Lock()
	defer health.Unlock()

	if err != nil {
		entry = HealthLogEntry{
			Start:    start,
			End:      end,
			ExitCode: 1,
			Output:   fmt.Sprintf("Error getting IP: %v", err),
		}
		health.Status = "unhealthy"
		health.FailingStreak++
	} else {
		if ip != ipCache {
			if err := updateDDNS(ip); err != nil {
				entry = HealthLogEntry{
					Start:    start,
					End:      end,
					ExitCode: 1,
					Output:   fmt.Sprintf("DDNS update failed: %v", err),
				}
				health.Status = "unhealthy"
				health.FailingStreak++
			} else {
				ipCache = ip
				entry = HealthLogEntry{
					Start:    start,
					End:      end,
					ExitCode: 0,
					Output:   fmt.Sprintf("DDNS updated successfully with IP: %s", ip),
				}
				log.Printf("DDNS updated successfully with IP: %s", ip)
				health.Status = "healthy"
				health.FailingStreak = 0
				health.CurrentIP = ip
			}
		} else {
			entry = HealthLogEntry{
				Start:    start,
				End:      end,
				ExitCode: 0,
				Output:   fmt.Sprintf("IP unchanged: %s", ip),
			}
			log.Printf("IP unchanged: %s", ip)
			health.Status = "healthy"
			health.FailingStreak = 0
		}
	}

	health.Log = append(health.Log, entry)
	if len(health.Log) > 10 {
		health.Log = health.Log[1:]
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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	health.RLock()
	defer health.RUnlock()

	// Set proper HTTP status code
	switch health.Status {
	case "healthy":
		w.WriteHeader(http.StatusOK)
	case "unhealthy":
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		w.WriteHeader(http.StatusOK)
	}

	json.NewEncoder(w).Encode(struct {
		Status        string           `json:"Status"`
		FailingStreak int              `json:"FailingStreak"`
		Log           []HealthLogEntry `json:"Log"`
	}{
		Status:        health.Status,
		FailingStreak: health.FailingStreak,
		Log:           health.Log,
	})
}
