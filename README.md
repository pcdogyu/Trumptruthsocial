# TruthSocial Monitor

Go-based Truth Social monitor with a Telegram forwarder and a web UI.

## What Changed

- The runtime is now Go-only.
- The token helper is a Go subcommand: `go run . get-token`
- Windows and Ubuntu/Linux both use the same code path.
- The app keeps the current Bearer Token plus the previous two tokens.
- Token refresh happens automatically on startup when the token expires.

## Quick Start

```bash
git clone https://github.com/your-username/Trumptruthsocial.git
cd Trumptruthsocial
cp config.yaml.example config.yaml
go run . get-token
go run .
```

Open `http://127.0.0.1:8085` after the server starts.

## Ubuntu / Linux

Install Go and a Chromium-compatible browser:

```bash
sudo apt update
sudo apt install -y golang chromium-browser
```

If your distro does not provide `chromium-browser`, install `chromium` or `google-chrome-stable` instead.

If the browser binary is not on the default path, set:

```bash
export TRUTHSOCIAL_CHROME_PATH=/usr/bin/google-chrome
```

If you need to force a visible browser for token login:

```bash
export TRUTHSOCIAL_TOKEN_HEADLESS=0
```

## Windows

- Double-click `start.bat`, or run `truthsocial.exe`.
- The app will refresh the token automatically when needed.

## Configuration

- `auth.bearer_token_validity_days` defaults to 5.
- `auth.bearer_token_backup_1` and `auth.bearer_token_backup_2` store the previous two tokens.
- The content page shows pagination with 10 posts per page.
- The token display in the UI is masked with 10 asterisks in the middle.

## Chinese

### 简介

这是一个 Go 版本的 Truth Social 监控工具，带 Telegram 转发和 Web 管理界面。

### 运行方式

```bash
cp config.yaml.example config.yaml
go run . get-token
go run .
```

### Ubuntu 说明

```bash
sudo apt update
sudo apt install -y golang chromium-browser
```

如果浏览器路径不是默认值，可以设置：

```bash
export TRUTHSOCIAL_CHROME_PATH=/usr/bin/google-chrome
```

如果需要强制使用可见浏览器来登录并抓 token：

```bash
export TRUTHSOCIAL_TOKEN_HEADLESS=0
```

### Windows 说明

- 直接双击 `start.bat`
- 或运行 `truthsocial.exe`

### 说明

- 启动时会自动检查 Bearer Token 是否过期。
- 过期后会自动打开浏览器抓取新 token，并写回 `config.yaml`。
- 会保留最近 3 个 Bearer Token，便于自动回退。
- 内容页默认每页 10 条，并在表格上下都显示分页组件。
