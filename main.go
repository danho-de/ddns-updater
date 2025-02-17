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

// Config remains unchanged.
type Config struct {
	User       string `json:"user"`
	Pass       string `json:"pass"`
	Ddns       string `json:"ddns"`
	Interval   int    `json:"interval"`    // default 300
	HealthPort int    `json:"health_port"` // default 8080
}

// Updated HealthStatus includes FailingStreak and Log.
type HealthStatus struct {
	sync.RWMutex
	Healthy       bool             `json:"healthy"`
	LastUpdate    time.Time        `json:"last_update"`
	LastError     string           `json:"last_error"`
	CurrentIP     string           `json:"current_ip"`
	StartTime     time.Time        `json:"-"`
	Interval      int              `json:"interval"`
	FailingStreak int              `json:"failing_streak"`
	Log           []HealthLogEntry `json:"log"`
}

// HealthLogEntry holds details for each IP check attempt.
type HealthLogEntry struct {
	Start    time.Time `json:"Start"`
	End      time.Time `json:"End"`
	ExitCode int       `json:"ExitCode"`
	Output   string    `json:"Output"`
}

// HealthResponse and HealthState represent the new output structure.
type HealthResponse struct {
	Created time.Time   `json:"Created"`
	Path    string      `json:"Path"`
	Args    []string    `json:"Args"`
	State   HealthState `json:"State"`
}

type HealthState struct {
	Status        string           `json:"Status"`        // "starting", "healthy", or "unhealthy"
	FailingStreak int              `json:"FailingStreak"` // current failing streak count
	Log           []HealthLogEntry `json:"Log"`           // recent log entries
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

// loadConfig and handleConfigChanges remain unchanged.
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
	// Record the start time of the check.
	start := time.Now()

	ip, err := getPublicIP()
	end := time.Now()
	var exitCode int
	var output string

	if err != nil {
		exitCode = 1
		output = fmt.Sprintf("Error getting IP: %v", err)
		health.Lock()
		health.Healthy = false
		health.LastError = err.Error()
		health.FailingStreak++
		health.Unlock()
		log.Printf("Error getting IP: %v", err)
	} else {
		// Successful IP fetch
		if ip != ipCache {
			if err := updateDDNS(ip); err != nil {
				exitCode = 1
				output = fmt.Sprintf("DDNS update failed: %v", err)
				health.Lock()
				health.Healthy = false
				health.LastError = err.Error()
				health.FailingStreak++
				health.Unlock()
				log.Printf("DDNS update failed: %v", err)
			} else {
				ipCache = ip
				exitCode = 0
				output = fmt.Sprintf("DDNS updated successfully with IP: %s", ip)
				health.Lock()
				health.Healthy = true
				health.LastError = ""
				health.FailingStreak = 0
				health.CurrentIP = ip
				health.LastUpdate = time.Now()
				health.Unlock()
				log.Printf("DDNS updated successfully with IP: %s", ip)
			}
		} else {
			exitCode = 0
			output = fmt.Sprintf("IP unchanged: %s", ip)
			health.Lock()
			health.Healthy = true
			health.LastError = ""
			health.FailingStreak = 0
			health.LastUpdate = time.Now()
			health.Unlock()
			log.Printf("IP unchanged: %s", ip)
		}
	}

	// Append a new log entry for this check.
	health.Lock()
	health.Log = append(health.Log, HealthLogEntry{
		Start:    start,
		End:      end,
		ExitCode: exitCode,
		Output:   output,
	})
	// Optionally, limit the log slice to the last 10 entries.
	if len(health.Log) > 10 {
		health.Log = health.Log[len(health.Log)-10:]
	}
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

// healthHandler now builds and returns the new JSON structure.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	health.RLock()
	defer health.RUnlock()

	// Determine the status: if no update has yet occurred, show "starting"
	status := "starting"
	if !health.LastUpdate.IsZero() {
		if health.Healthy {
			status = "healthy"
		} else {
			status = "unhealthy"
		}
	}

	response := HealthResponse{
		Created: health.StartTime,
		Path:    r.URL.Path, // will be "/health"
		Args:    os.Args,    // returns the command-line arguments
		State: HealthState{
			Status:        status,
			FailingStreak: health.FailingStreak,
			Log:           health.Log,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
