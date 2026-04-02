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

## Ubuntu systemd service

Install the service file and enable it:

```bash
sudo bash scripts/install-systemd.sh
```

The service runs from `/opt/Trumptruthsocial` and starts `truthsocial.exe`.
To use the in-app upgrade button, the server must also have `git` and `go` installed.

The upgrade flow is:

1. Click `升级` on the content page.
2. The app starts `upgrade.sh` through `systemd-run`.
3. `upgrade.sh` runs `git pull --ff-only origin golang`.
4. `upgrade.sh` runs `go build -o truthsocial.exe .`.
5. `upgrade.sh` runs `systemctl restart truthsocial.service`.

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

### Ubuntu systemd 服务

安装并启用服务：

```bash
sudo bash scripts/install-systemd.sh
```

服务默认运行在 `/opt/Trumptruthsocial`，启动 `truthsocial.exe`。
如果要使用页面里的 `升级` 菜单，服务器还需要安装 `git` 和 `go`，因为升级流程会在服务器上直接执行构建。

升级流程：

1. 在内容页点击 `升级`
2. 程序通过 `systemd-run` 拉起 `upgrade.sh`
3. `upgrade.sh` 执行 `git pull --ff-only origin golang`
4. `upgrade.sh` 执行 `go build -o truthsocial.exe .`
5. `upgrade.sh` 执行 `systemctl restart truthsocial.service`

### Windows 说明

- 直接双击 `start.bat`
- 或运行 `truthsocial.exe`

### 说明

- 启动时会自动检查 Bearer Token 是否过期。
- 过期后会自动打开浏览器抓取新 token，并写回 `config.yaml`。
- 会保留最近 3 个 Bearer Token，便于自动回退。
- 内容页默认每页 10 条，并在表格上下都显示分页组件。
