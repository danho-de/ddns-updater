# DDNS Updater

A lightweight Go application that monitors your public IP and updates your DDNS provider when changes are detected. It supports dynamic configuration reloading, graceful shutdown.

## Features

- **Dynamic DDNS Updating:**  
  Periodically checks your public IP (via [api.ipify.org](https://api.ipify.org)) and updates your DDNS record if the IP changes.

- **Configurable:**  
  Loads settings from a JSON configuration file (`config/config.json`) and automatically reloads when changes are detected.

- **Graceful Shutdown:**  
  Listens for interrupt signals (Ctrl+C) and shuts down cleanly.

## Prerequisites

- Go 1.16+ (or newer)
- Git (if building from source)
- [fsnotify](https://github.com/fsnotify/fsnotify) Go package

## Installation

**Clone the repository (if applicable):**

```
git clone https://github.com/danho-de/ddns-updater.git
cd your-ddns-updater
```

## Configuration

**Create a configuration file at config/config.json with the following structure:**

  ```
    {
    "user": "your-username",
    "pass": "your-password",
    "ddns": "your-ddns-server.com",
    "interval": 300,
    }
  ```

- user: Your DDNS service username.
- pass: Your DDNS service password.
- ddns: The DDNS service endpoint (host/path) used in the update URL.
- interval: Update interval in seconds (defaults to 300 seconds if set below 1).
## Build Instructions

A build script (build.sh) is provided to format and compile the application with a build number derived from the git commit history. The build is statically linked for Linux using CGO_ENABLED=0.

To build the application, simply run:

```
./build.sh
```

This script will produce a binary named ddns-updater.

## Running the Application

1- Ensure the configuration file is in place at config/config.json.

2- Start the application:

  ```
  ./ddns-updater
  ```


The application will:

- Monitor your public IP.
- Update your DDNS record when a change is detected.
- Watch for configuration file changes and reload settings automatically.

## Docker Deployment

The repository includes a Dockerfile for containerizing the application. The Docker build uses a multi-stage process:

1. Alpine Stage:
   Installs CA certificates in Alpine.

2. Scratch Stage:
   Copies the CA certificates from Alpine into a minimal scratch image along with the ddnsclddns-updaterient binary and configuration files.

## Building the Docker Image

To build the Docker image, run:

```
    docker build -t ddns-updater .
```

## Running the Docker Container

```
    docker run -d --name ddns-updater ddns-updater
```

## Contributing

Contributions are welcome! If you encounter issues or have suggestions for improvements, please open an issue or submit a pull request.

## License

This project is licensed under the MIT License. See the LICENSE file for details.
