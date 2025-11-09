use chrono::{DateTime, Local};
use log::{error, info, warn};
use notify::{Config as NotifyConfig, RecommendedWatcher, RecursiveMode, Watcher};
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;
use tokio::fs;
use tokio::sync::{mpsc, RwLock};
use tokio::time::{interval, sleep};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
struct Config {
    user: String,
    pass: String,
    ddns: String,
    #[serde(default = "default_interval")]
    interval: u64,
}

fn default_interval() -> u64 {
    300
}

impl Config {
    fn is_valid(&self) -> bool {
        !self.user.is_empty() && !self.pass.is_empty() && !self.ddns.is_empty()
    }

    fn normalize(&mut self) {
        if self.interval < 60 {
            self.interval = 300;
        }
    }
}

struct AppState {
    config: Arc<RwLock<Option<Config>>>,
    ip_cache: Arc<RwLock<Option<String>>>,
    last_change_time: Arc<RwLock<Option<DateTime<Local>>>>,
    client: reqwest::Client,
}

impl AppState {
    fn new() -> Self {
        Self {
            config: Arc::new(RwLock::new(None)),
            ip_cache: Arc::new(RwLock::new(None)),
            last_change_time: Arc::new(RwLock::new(None)),
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(10))
                .build()
                .unwrap(),
        }
    }
}

enum ConfigLoadResult {
    Success,
    InvalidConfig,
    FileError,
    NoChange,
}

#[tokio::main]
async fn main() {
    env_logger::init_from_env(env_logger::Env::new().default_filter_or("info"));

    let state = Arc::new(AppState::new());
    let config_path = "config/config.json";

    // Load initial config
    match load_config(config_path, state.clone(), true).await {
        ConfigLoadResult::Success => {
            tokio::spawn(start_ip_checker(state.clone()));
        }
        _ => {
            error!("Failed to load initial config. Please fix config.json and restart.");
        }
    }

    // Watch config file
    tokio::spawn(watch_config(config_path.to_string(), state.clone()));

    // Keep main thread alive
    tokio::signal::ctrl_c().await.ok();
    info!("Shutting down...");
}

async fn load_config(path: &str, state: Arc<AppState>, first_load: bool) -> ConfigLoadResult {
    match fs::read_to_string(path).await {
        Ok(contents) => match serde_json::from_str::<Config>(&contents) {
            Ok(mut new_config) => {
                new_config.normalize();

                if !new_config.is_valid() {
                    error!("✗ Invalid config: user, pass, or ddns is missing!");
                    error!("Current config:");
                    error!(
                        "  - user: '{}'",
                        if new_config.user.is_empty() {
                            "<empty>"
                        } else {
                            &new_config.user
                        }
                    );
                    error!(
                        "  - pass: '{}'",
                        if new_config.pass.is_empty() {
                            "<empty>"
                        } else {
                            "<set>"
                        }
                    );
                    error!(
                        "  - ddns: '{}'",
                        if new_config.ddns.is_empty() {
                            "<empty>"
                        } else {
                            &new_config.ddns
                        }
                    );
                    return ConfigLoadResult::InvalidConfig;
                }

                let mut config_guard = state.config.write().await;
                let config_changed = config_guard.as_ref() != Some(&new_config);

                if first_load {
                    *config_guard = Some(new_config.clone());
                    info!("✓ Config loaded successfully");
                    return ConfigLoadResult::Success;
                }

                if config_changed {
                    *config_guard = Some(new_config.clone());
                    info!("✓ Config changed and reloaded");
                    return ConfigLoadResult::Success;
                }

                ConfigLoadResult::NoChange
            }
            Err(e) => {
                error!("✗ JSON Parse Error: {}", e);
                error!("File: {}", path);
                error!("Please check your JSON syntax (commas, quotes, brackets)");
                ConfigLoadResult::InvalidConfig
            }
        },
        Err(e) => {
            error!("✗ File Read Error: {}", e);
            error!("File: {}", path);
            ConfigLoadResult::FileError
        }
    }
}

async fn watch_config(config_path: String, state: Arc<AppState>) {
    let (tx, mut rx) = mpsc::channel(1);

    let mut watcher = RecommendedWatcher::new(
        move |res| {
            tx.blocking_send(res).ok();
        },
        NotifyConfig::default(),
    )
    .expect("Failed to create watcher");

    loop {
        match watcher.watch(Path::new(&config_path), RecursiveMode::NonRecursive) {
            Ok(_) => {
                info!("Watching config file for changes...");
                break;
            }
            Err(e) => {
                warn!("Failed to watch config: {}. Retrying in 10 seconds...", e);
                sleep(Duration::from_secs(10)).await;
            }
        }
    }

    while let Some(event) = rx.recv().await {
        match event {
            Ok(event) => {
                if event.kind.is_modify() {
                    match load_config(&config_path, state.clone(), false).await {
                        ConfigLoadResult::Success => {
                            info!("✓ Config reloaded successfully");
                            tokio::spawn(check_and_update_ip(state.clone()));
                        }
                        ConfigLoadResult::InvalidConfig => {
                            warn!("✗ Config has validation errors - keeping previous valid config");
                            warn!("Fix the config values and save again");
                        }
                        ConfigLoadResult::FileError => {
                            error!("✗ Cannot read config file - keeping previous valid config");
                        }
                        ConfigLoadResult::NoChange => {
                            info!("Config file saved but no changes detected");
                        }
                    }
                }
            }
            Err(e) => error!("Watch error: {:?}", e),
        }
    }
}

async fn start_ip_checker(state: Arc<AppState>) {
    loop {
        let config = {
            let config_guard = state.config.read().await;
            match config_guard.clone() {
                Some(c) => c,
                None => {
                    sleep(Duration::from_secs(5)).await;
                    continue;
                }
            }
        };

        let check_interval = Duration::from_secs(config.interval);
        let mut ticker = interval(check_interval);

        // Initial check
        check_and_update_ip(state.clone()).await;

        loop {
            ticker.tick().await;

            // Check if config has changed
            let current_config = state.config.read().await.clone();
            if current_config != Some(config.clone()) {
                info!("Config changed detected, restarting IP checker with new interval");
                break;
            }

            check_and_update_ip(state.clone()).await;
        }
    }
}

async fn check_and_update_ip(state: Arc<AppState>) {
    // First check if we have internet connectivity
    if let Err(e) = check_internet_connectivity(&state.client).await {
        error!("✗ No internet connection: {}", e);
        return;
    }

    let ip = match get_public_ip(&state.client).await {
        Ok(ip) => ip,
        Err(e) => {
            error!("✗ Failed to get public IP: {}", e);
            if e.to_string().contains("dns")
                || e.to_string().contains("connect")
                || e.to_string().contains("timeout")
            {
                error!("⚠ Network issue detected - will retry at next interval");
            }
            return;
        }
    };

    let ip_cache = state.ip_cache.read().await;
    if ip_cache.as_ref() == Some(&ip) {
        let last_change = state.last_change_time.read().await;
        if let Some(time) = *last_change {
            info!(
                "✓ IP unchanged: {} (last changed {})",
                ip,
                time.format("%Y-%m-%d %H:%M:%S")
            );
        } else {
            info!("✓ IP unchanged: {} (change time unknown)", ip);
        }
        return;
    }
    drop(ip_cache);

    info!("⚠ IP changed to: {}", ip);

    let config = {
        let config_guard = state.config.read().await;
        match config_guard.as_ref() {
            Some(c) => c.clone(),
            None => {
                error!("✗ No valid config available");
                return;
            }
        }
    };

    if let Err(e) = update_ddns(&state.client, &config, &ip).await {
        error!("✗ DDNS update failed: {}", e);
        if e.to_string().contains("401") || e.to_string().contains("403") {
            error!("⚠ Authentication failed - check username/password in config");
        } else if e.to_string().contains("dns")
            || e.to_string().contains("connect")
            || e.to_string().contains("timeout")
        {
        } else if e.to_string().contains("404") {
            error!("⚠ DDNS provider not found - check ddns URL in config");
        }
        return;
    }

    *state.ip_cache.write().await = Some(ip.clone());
    *state.last_change_time.write().await = Some(Local::now());
    info!("✓ DDNS updated successfully with IP: {}", ip);
}

async fn check_internet_connectivity(
    client: &reqwest::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    // Try to connect to a reliable endpoint (Cloudflare DNS)
    client
        .get("https://1.1.1.1")
        .timeout(Duration::from_secs(5))
        .send()
        .await
        .map_err(|e| {
            if e.is_timeout() {
                "connection timeout - no internet".to_string()
            } else if e.is_connect() {
                "cannot connect - no internet".to_string()
            } else {
                format!("connectivity check failed: {}", e)
            }
        })?;

    Ok(())
}

async fn get_public_ip(client: &reqwest::Client) -> Result<String, Box<dyn std::error::Error>> {
    let resp = client
        .get("https://api.ipify.org")
        .send()
        .await
        .map_err(|e| {
            if e.is_timeout() {
                "timeout - check internet connection".to_string()
            } else if e.is_connect() {
                "connection failed - check internet connection".to_string()
            } else {
                format!("network error: {}", e)
            }
        })?;

    if !resp.status().is_success() {
        return Err(format!("API returned status: {}", resp.status()).into());
    }

    let ip = resp.text().await?;
    Ok(ip.trim().to_string())
}

async fn update_ddns(
    client: &reqwest::Client,
    config: &Config,
    ip: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    let url = format!(
        "https://{}:{}@{}?myip={}",
        config.user, config.pass, config.ddns, ip
    );

    let resp = client.get(&url).send().await.map_err(|e| {
        if e.is_timeout() {
            "timeout - check internet connection".to_string()
        } else if e.is_connect() {
            "connection failed - check ddns provider".to_string()
        } else {
            format!("request error: {}", e)
        }
    })?;

    let status = resp.status();
    if !status.is_success() {
        return Err(format!(
            "status: {} ({})",
            status.as_u16(),
            status.canonical_reason().unwrap_or("Unknown")
        )
        .into());
    }

    Ok(())
}
