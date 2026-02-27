---
name: browserwing-install
description: How to install, configure, and start BrowserWing, and how to set up Executor Skill and Admin Skill for AI agent integration.
---

# BrowserWing Installation & Setup Guide

This guide walks you through installing BrowserWing, configuring it, starting the service, and installing Skills so that AI agents can manage and use BrowserWing.

---

## 1. Install BrowserWing

Choose one of the following installation methods:

### Method A: One-Line Install Script (Recommended)

**Linux / macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/browserwing/browserwing/main/install.sh | bash
```

**Windows (PowerShell):**
```powershell
iwr -useb https://raw.githubusercontent.com/browserwing/browserwing/main/install.ps1 | iex
```

The script automatically detects your OS and architecture, downloads the latest release binary, and installs it to `~/.browserwing/`.

### Method B: Install via npm

```bash
npm install -g browserwing
```

### Method C: Download Binary Manually

Download the appropriate binary from: https://github.com/browserwing/browserwing/releases

| Platform | File |
|----------|------|
| Linux x64 | `browserwing-linux-amd64` |
| Linux ARM64 | `browserwing-linux-arm64` |
| macOS Intel | `browserwing-darwin-amd64` |
| macOS Apple Silicon | `browserwing-darwin-arm64` |
| Windows x64 | `browserwing-windows-amd64.exe` |
| Windows ARM64 | `browserwing-windows-arm64.exe` |

After downloading, make it executable (Linux/macOS):
```bash
chmod +x browserwing-linux-amd64
```

### Method D: Build from Source

Requires Go 1.21+ and Node.js 18+ with pnpm.

```bash
git clone https://github.com/browserwing/browserwing.git
cd browserwing

# Install dependencies
make install

# Build embedded version (frontend bundled into backend binary)
make build-embedded

# The binary is at: build/browserwing
```

---

## 2. Install Google Chrome (Required Dependency)

BrowserWing requires Google Chrome (or Chromium) for browser automation.

> **Windows & macOS users:** If you already have Google Chrome installed on your system (most people do), you can **skip this step**. BrowserWing will automatically detect Chrome in its standard installation path:
> - **Windows:** `C:\Program Files\Google\Chrome\Application\chrome.exe` or `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`
> - **macOS:** `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`
>
> Only if Chrome is not found will you need to install it manually.

### Linux (Typically Needs Manual Install)

Linux servers usually don't have Chrome pre-installed. Install it with:

```bash
wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | sudo apt-key add -
echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" | sudo tee /etc/apt/sources.list.d/google-chrome.list
sudo apt-get update
sudo apt-get install -y google-chrome-stable
```

### macOS (Only If Not Already Installed)
```bash
brew install --cask google-chrome
```

### Windows (Only If Not Already Installed)
Download and install from: https://www.google.com/chrome/

### Custom Chrome Path

If Chrome is installed in a non-standard location, tell BrowserWing where to find it via `config.toml` or environment variable:

```toml
# config.toml
[browser]
bin_path = "/path/to/your/chrome"
```

Or:
```bash
export CHROME_BIN_PATH="/path/to/your/chrome"
```

---

## 3. Configure BrowserWing

BrowserWing uses a `config.toml` file. If no config file exists, it uses sensible defaults and auto-detects Chrome.

### Create Config (Optional)

Create a `config.toml` in the same directory as the binary:

```toml
# Server settings
[server]
host = "0.0.0.0"
port = "8080"

# Database
[database]
path = "./data/browserwing.db"

# Browser settings
[browser]
bin_path = ""                    # Leave empty to auto-detect Chrome
user_data_dir = "./chrome_user_data"  # Stores login sessions, cookies, cache
# control_url = ""              # Set this to connect to a remote Chrome instance

# Logging
[log]
level = "info"
file = "./logs/browserwing.log"
```

### Key Configuration Options

| Setting | Description | Default |
|---------|-------------|---------|
| `server.port` | HTTP server port | `8080` |
| `browser.bin_path` | Chrome binary path (auto-detected if empty) | `""` |
| `browser.user_data_dir` | Chrome user data directory for persistent sessions | `./chrome_user_data` |
| `browser.control_url` | Remote Chrome DevTools URL (overrides local Chrome) | `""` |
| `log.file` | Log file path | `./logs/browserwing.log` |

### Environment Variables

- `CHROME_BIN_PATH` — Override Chrome binary location

---

## 4. Start BrowserWing

### Basic Start
```bash
./browserwing --port 8080
```

### With Custom Config
```bash
./browserwing --config ./config.toml --port 8080
```

### Verify the Service is Running
```bash
curl http://localhost:8080/health
```

Expected response:
```json
{"status": "ok"}
```

### Access the Web UI

Open in your browser: http://localhost:8080

The Web UI provides a visual interface for managing scripts, browser instances, and AI features.

---

## 5. Set Up LLM (Required for AI Features)

AI-powered features (AI Explorer, Agent chat, smart extraction) need an LLM configuration.

```bash
curl -X POST 'http://localhost:8080/api/v1/llm-configs' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-llm",
    "provider": "openai",
    "api_key": "sk-your-api-key",
    "model": "gpt-4o",
    "base_url": "https://api.openai.com/v1",
    "is_active": true,
    "is_default": true
  }'
```

**Supported providers:** `openai`, `anthropic`, `deepseek`, or any OpenAI-compatible endpoint.

Test the connection:
```bash
curl -X POST 'http://localhost:8080/api/v1/llm-configs/test' \
  -H 'Content-Type: application/json' \
  -d '{"name": "my-llm"}'
```

---

## 6. Install Skills for AI Agent Integration

BrowserWing provides two types of Skills that can be installed into AI agents (e.g., Claude, Cursor, or any agent that supports SKILL.md files):

### Skill 1: Admin Skill (Full Platform Management)

The Admin Skill gives an AI agent the ability to manage BrowserWing end-to-end: configure LLM, create/edit/delete scripts, run AI exploration, execute scripts, export skills, and troubleshoot.

**Download via API (dynamic, uses your actual host):**
```bash
curl -o SKILL_ADMIN.md 'http://localhost:8080/api/v1/admin/export/skill'
```

**Or use the static file included in the repository:**
```bash
cp SKILL_ADMIN.md /path/to/your/agent/skills/
```

### Skill 2: Executor Skill (Browser Control API)

The Executor Skill gives an AI agent direct browser control capabilities: navigate pages, click elements, type text, extract data, take screenshots, run JavaScript, and more.

**Download via API:**
```bash
curl -o SKILL_EXECUTOR.md 'http://localhost:8080/api/v1/executor/export/skill'
```

**Or use the static file included in the repository:**
```bash
cp SKILL_EXECUTOR.md /path/to/your/agent/skills/
```

### Skill 3: Script Skill (Your Custom Scripts)

Export your automation scripts as a Skill so AI agents can discover and execute them:

**Export all scripts:**
```bash
curl -X POST 'http://localhost:8080/api/v1/scripts/export/skill' \
  -H 'Content-Type: application/json' \
  -d '{"script_ids": []}' \
  -o SKILL_SCRIPTS.md
```

**Export selected scripts:**
```bash
curl -X POST 'http://localhost:8080/api/v1/scripts/export/skill' \
  -H 'Content-Type: application/json' \
  -d '{"script_ids": ["script-id-1", "script-id-2"]}' \
  -o SKILL_SCRIPTS.md
```

### Where to Place Skill Files

Place the downloaded `.md` files into your AI agent's skill/knowledge directory:

| Agent | Skill Directory |
|-------|----------------|
| Cursor | Project root or `.cursor/skills/` directory |
| Claude Desktop | Upload as project knowledge |
| Custom Agent | Wherever your agent reads tool/skill definitions |

---

## 7. Quick Verification Checklist

After installation and setup, verify everything works:

```bash
# 1. Check service health
curl http://localhost:8080/health

# 2. Check if Chrome is accessible (browser auto-starts on first use)
curl http://localhost:8080/api/v1/browser/instances

# 3. List LLM configs (should show your configured LLM)
curl http://localhost:8080/api/v1/llm-configs

# 4. Test browser automation
curl -X POST 'http://localhost:8080/api/v1/executor/navigate' \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://example.com"}'

# 5. Get page snapshot (verify browser is working)
curl http://localhost:8080/api/v1/executor/snapshot

# 6. List available scripts
curl http://localhost:8080/api/v1/scripts
```

If all checks pass, BrowserWing is fully operational and ready for AI agent integration.

---

## Troubleshooting

### Chrome won't start
```bash
# Check if Chrome is installed
google-chrome --version

# Check for stale lock files
ls -la ./chrome_user_data/SingletonLock 2>/dev/null
rm -f ./chrome_user_data/SingletonLock ./chrome_user_data/SingletonCookie ./chrome_user_data/SingletonSocket

# Kill lingering Chrome processes
pkill -f chrome
```

### Port already in use
```bash
# Check what's using port 8080
lsof -i :8080
# or
netstat -tlnp | grep 8080

# Use a different port
./browserwing --port 9090
```

### View logs
```bash
tail -f ./logs/browserwing.log
```

### Service not responding
```bash
# Check if process is running
ps aux | grep browserwing

# Restart the service
pkill browserwing
./browserwing --port 8080
```
