# DDNS Updater

A robust Rust application that continuously monitors your public IP and updates your DDNS provider when changes are detected. Features automatic recovery, configuration validation, and error resilience.

## Features

- **Reliable IP Monitoring:**  
  Continuously checks your public IP (via [api.ipify.org](https://api.ipify.org)) with automatic retries.

- **Error Resilience:**  
  Survives configuration errors and network outages while providing clear error messages.

- **Automatic Configuration:**  
  - Hot-reload of config changes without restart
  - Validation of required fields
  - Self-healing when invalid config becomes valid

- **Intelligent IP Tracking:**  
  - Records timestamp of last IP change
  - Displays when IP was last changed in logs
  - Provides context for unchanged IP addresses

- **Safe Operation:**  
  - Maintains last known good configuration
  - Graceful shutdown handling (Ctrl+C)
  - Prevents duplicate update requests

- **Network Resilience:**
  - Pre-flight connectivity checks
  - Automatic retry on network failures
  - Clear distinction between config and network errors

- **DDNS Protocol Support:**  
  Standard HTTP-based (dyndns2 protocol e.g. https://user:pass@ddns?myip=ip) update mechanism compatible with most providers.

## Prerequisites

- Rust 1.70+ (for building from source)
- musl toolchain (for static linking)
- Docker & Docker Compose (for containerized deployment)

## Installation

**Clone the repository:**

```bash
git clone https://github.com/danho-de/ddns-updater.git
cd ddns-updater
```

## Configuration

**Create a configuration file at `config/config.json` with the following structure:**

```json
{
  "user": "your-username",
  "pass": "your-password",
  "ddns": "your-ddns.provider.com",
  "interval": 300
}
```

- **Required Fields** (`user`, `pass`, `ddns`):  
  Authentication credentials and DDNS endpoint.
- **interval**: Update check frequency in seconds (minimum 60, defaults to 300).

## Build Instructions

### First-Time Setup

Install the musl toolchain for static linking:

```bash
./setup-musl.sh
```

### Building

A build script (`build.sh`) is provided to format and compile the application as a statically-linked binary for Linux.

**Using build script:**
```bash
./build.sh  # Creates statically-linked binary
```

**Manual build:**
```bash
cargo build --release --target x86_64-unknown-linux-musl
```

## Running the Application

1. Ensure the configuration file is in place at `config/config.json`.

2. Start the application:

```bash
./ddns-updater
```

The application will:
- Validate configuration on startup
- Continue running with last valid config if errors occur
- Automatically recover when configuration issues are fixed
- Perform immediate IP check when config changes
- Check internet connectivity before attempting updates
- Log all update attempts and configuration changes

## Docker Deployment

The repository includes a Dockerfile for containerizing the application. The Docker build uses a multi-stage process:

1. **Alpine Stage:**  
   Installs CA certificates in Alpine.

2. **Scratch Stage:**  
   Copies the CA certificates from Alpine into a minimal scratch image along with the statically-linked Rust binary and configuration directory.

### Building the Docker Image

To build the Docker image, run:

```bash
docker build -t ddns-updater .
```

### Running the Docker Container

```bash
docker run -d \
  --restart unless-stopped \
  --name ddns-updater \
  -v $(pwd)/config:/app/config \
  ddns-updater
```

**Docker Features:**
- Minimal scratch-based image (~5MB)
- Statically-linked Rust binary with rustls (no OpenSSL dependency)
- Certificate bundle included
- Config volume for persistent settings
- Auto-restart policy

## Why Rust?

This project was migrated from Go to Rust for:
- **Better Performance:** Lower memory footprint and faster execution
- **Memory Safety:** Compile-time guarantees prevent common bugs
- **Smaller Binaries:** Statically-linked musl builds are extremely compact
- **Modern Async Runtime:** Tokio provides excellent async I/O performance
- **Type Safety:** Strong type system catches errors at compile time

## Project Structure

```
.
├── src/
│   └── main.rs           # Rust application
├── config/
│   └── config.json       # Configuration file
├── Cargo.toml            # Rust dependencies
├── .cargo/
│   └── config.toml       # Cargo build config for musl
├── build.sh              # Build script for musl static binary
├── setup-musl.sh         # One-time musl toolchain setup
├── Dockerfile            # Minimal scratch-based image
└── docker-compose.yml    # Docker Compose configuration
```

## Error Handling

The application provides clear feedback for different error types:

- **✗ Invalid config:** Missing or empty required fields
- **✗ JSON Parse Error:** Syntax errors in config.json
- **✗ No internet connection:** Pre-flight connectivity check failed
- **⚠ Network issue:** Temporary connectivity problems (auto-retry)
- **⚠ Authentication failed:** Invalid credentials (check config)
- **✓ Success:** IP check or DDNS update successful

## Contributing

Contributions are welcome! If you encounter issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the MIT [License](https://github.com/danho-de/ddns-updater/blob/main/LICENSE). See the LICENSE file for details.