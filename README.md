# DDNS Updater

A robust Go application that continuously monitors your public IP and updates your DDNS provider when changes are detected. Features automatic recovery, configuration validation, and error resilience.

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

  - **DDNS Protocol Support:**  
    Standard HTTP-based (dyndns2 protocol e.g. https://user:pass@ddns?myip=ip) update mechanism compatible with most providers.

## Prerequisites

- Go 1.16+ (or newer)
- Git (if building from source)
- [fsnotify](https://github.com/fsnotify/fsnotify) Go package

## Installation

**Clone the repository (if applicable):**

```bash
  git clone https://github.com/danho-de/ddns-updater.git
  cd your-ddns-updater
```

## Configuration

**Create a configuration file at config/config.json with the following structure:**

  ```json
    {
    "user": "your-username",
    "pass": "your-password",
    "ddns": "your-ddns.provider",
    "interval": 300,
    }
  ```

- Required Fields (user, pass, ddns):  
  Authentication credentials and DDNS endpoint.
- interval: Update check frequency in seconds (minimum 60, defaults to 300).

## Build Instructions

A build script (build.sh) is provided to format and compile the application with a build number derived from the git commit history. The build is statically linked for Linux using CGO_ENABLED=0.

To build the application:

  Using build script:
  ```bash
    ./build.sh  # Creates Linux binary in bin/
  ```


  Manual build:
  ```bash
    go build -o ddns-updater main.go
  ```

## Running the Application

1- Ensure the configuration file is in place at config/config.json.

2- Start the application:

  ```bash
    ./ddns-updater
  ```

  The application will:
  - Validate configuration on startup.
  - Continue running with last valid config if errors occur.
  - Automatically recover when configuration issues are fixed.
  - Log all update attempts and configuration changes.

## Docker Deployment

The repository includes a Dockerfile for containerizing the application. The Docker build uses a multi-stage process:

1. Alpine Stage:
   Installs CA certificates in Alpine.

2. Scratch Stage:
   Copies the CA certificates from Alpine into a minimal scratch image along with the ddnsclddns-updaterient binary and configuration files.

## Building the Docker Image

To build the Docker image, run:

```bash
    docker build -t ddns-updater .
```

## Running the Docker Container

```bash
    docker run -d \
    --restart unless-stopped \
    --name ddns-updater \
    -v ./config:/config \
    ddns-updater
```

  Docker Features:
  - Minimal scratch-based image.
  - Certificate bundle included.
  - Config volume for persistent settings.
  - Auto-restart policy.

## Contributing

Contributions are welcome! If you encounter issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the MIT [License](https://github.com/danho-de/ddns-updater/blob/main/LICENSE). See the LICENSE file for details.
