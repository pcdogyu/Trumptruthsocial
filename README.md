# TruthSocial Monitor

## Go 版说明

当前 `golang` 分支已经加入 Go 版实现。启动方式是：

```bash
go run .
```

Go 版保留了原有页面和路由语义，配置仍读取 `config.yaml`，帖子数据改为本地 `posts.json` 持久化。

A Python-based tool to monitor user accounts on Truth Social for new posts and send real-time notifications to a Telegram chat. It includes a web-based UI for easy configuration and viewing post history.

---

## English

### Features

*   **Multi-Account Monitoring**: Monitors multiple Truth Social accounts simultaneously.
*   **Telegram Notifications**: Sends notifications to Telegram for new text and video posts. Telegram configuration is now managed on a dedicated "Message Push" page in the Web UI.
*   **Web UI**: A Flask-based web interface for managing monitored accounts, API keys, and other settings.
*   **AI Configuration Page**: Dedicated page in the Web UI for managing AI analysis settings, including API key and prompt.
*   **Configurable Web Selectors**: CSS selectors used for scraping can now be configured in `config.yaml`, making the tool more resilient to website structure changes.
*   **Authenticated Fetching**: Uses a Bearer Token to access content that may require authentication.
*   **Token Helper**: Includes a script (`get_token.py`) that opens a browser, waits for login, and writes the Bearer Token back to `config.yaml`.
*   **Content Viewer**: A dedicated Web UI page to browse historical posts from monitored accounts.
*   **Historical Sync**: A button in the Web UI to trigger a manual synchronization of recent (e.g., last 7 days) posts for all monitored accounts, using a more robust Selenium-based scraping method to fetch dynamically loaded content.
*   **Post History**: Stores a history of all fetched posts in a local SQLite database.
*   **Sync Latest Post**: A button in the Web UI to trigger a manual synchronization of the very latest post for all monitored accounts, using a faster requests-based method.
*   **History Viewer**: The Web UI includes a page to view post history, including embedded videos.

### Prerequisites

*   Python 3.x
*   A Telegram Bot and its Token.
*   The Chat ID of the Telegram group/channel where you want to receive notifications.

### Installation & Setup

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/your-username/Trumptruthsocial.git
    cd Trumptruthsocial
    ```

2.  **Install dependencies:**
    ```bash
    pip install -r requirements.txt
    ```

3.  **Configure the application:**
    *   Copy `config.yaml.example` to `config.yaml` (If the example file exists).
    *   **Get Bearer Token**: Run `python get_token.py`. This will open a Chrome browser. Log in to Truth Social manually if needed. After login, the script will automatically extract the token and write it back to `config.yaml`.
    *   **Fill `config.yaml`** (or use the Web UI later):
        *   `auth`: Paste the `bearer_token` you obtained.
        *   `telegram`: Telegram `bot_token` and `chat_id` are now configured via the "消息推送" (Message Push) page in the Web UI.
        *   `auth.truthsocial_username`: Your username on Truth Social (e.g., `pcdogyuhao`).
        *   `ai_analysis`: AI settings are now configured via the "AI 配置" (AI Config) page in the Web UI.
    python main.py
    ```

2.  The script will start the monitoring process in the background and launch the web UI.

3.  **Access the Web UI:** Open your browser and go to `http://127.0.0.1:8085`.

4.  Use the Web UI to add/remove accounts to monitor and adjust other settings.

    *   **AI Config Page**: Navigate to the "AI 配置" (AI Config) tab to configure AI settings.
    *   **Message Push Page**: Navigate to the "消息推送" (Message Push) tab to configure Telegram settings.
    *   **Content Page**: Navigate to the "内容" (Content) tab to view historical posts.
    *   **Sync Button**: On the "内容" page, click "同步最近7天内容" to fetch more historical data.
    *   **Sync Latest Button**: On the "内容" page, click "同步最近一条" to fetch the very latest post for all monitored accounts.
---

## 中文说明

### 项目简介

一个 Python 工具，用于监控 Truth Social 特定用户账号的新帖子，并将通知实时发送到指定的 Telegram 聊天。它还包含一个基于 Web 的图形界面，用于简化配置和查看历史记录。

### 功能特性

*   **多账户监控**: 支持同时监控多个 Truth Social 账户。
*   **Telegram 通知**: 当发现新帖子（文本或视频）时，发送通知到 Telegram。Telegram 配置现在通过 Web UI 中的“消息推送”页面进行管理。
*   **Web UI**: 提供一个基于 Flask 的 Web 界面，用于管理监控列表、API 密钥和其他设置。
*   **AI 配置页面**: Web UI 中新增专用页面，用于管理 AI 分析设置，包括 API 密钥和提示词。
*   **可配置的网页选择器**: 用于网页抓取的 CSS 选择器现在可以在 `config.yaml` 中配置，使工具更能适应网站结构的变化。
*   **认证抓取**: 使用 Bearer Token 获取需要登录才能查看的内容。
*   **Token 获取助手**: 包含一个辅助脚本 (`get_token.py`)，会在您登录后自动从浏览器中提取 Bearer Token，并写回 `config.yaml`。
*   **内容查看器**: 一个专门的 Web UI 页面，用于浏览监控账户的历史帖子。
*   **历史同步**: Web UI 中增加一个按钮，可以手动触发所有监控账户最近（例如，最近7天）帖子的同步。此功能现在使用更健壮的基于 Selenium 的抓取方法，能够获取动态加载的历史内容。
*   **历史记录**: 将所有抓取到的帖子历史记录保存在本地 SQLite 数据库中。
*   **同步最近一条**: Web UI 中增加一个按钮，可以手动触发所有监控账户的最新一条帖子的同步，使用更快的基于 requests 的方法。
*   **历史查看器**: Web UI 包含历史记录页面，可查看帖子内容和播放视频。

### 环境要求

*   Python 3.x
*   一个 Telegram 机器人及其 Token。
*   接收通知的 Telegram 聊天（群组/频道）的 Chat ID。

### 安装与配置

1.  **克隆仓库:**
    ```bash
    git clone https://github.com/your-username/Trumptruthsocial.git
    cd Trumptruthsocial
    ```

2.  **安装依赖:**
    ```bash
    pip install -r requirements.txt
    ```

3.  **配置程序:**
    *   将 `config.yaml.example` 复制为 `config.yaml` (如果示例文件存在)。
    *   **获取 Bearer Token**: 运行 `python get_token.py`。脚本会打开一个 Chrome 浏览器，请手动登录 Truth Social。登录成功后，脚本会自动提取 Token，并写回 `config.yaml`。
    *   **填写 `config.yaml`** (或稍后通过 Web UI 配置):
        *   `auth`: 粘贴你获取到的 `bearer_token`。
        *   `telegram`: Telegram 的 `bot_token` 和 `chat_id` 现在通过 Web UI 中的“消息推送”页面进行配置。
        *   `auth.truthsocial_username`: 您在 Truth Social 上的用户名（例如 `pcdogyuhao`）。
        *   `ai_analysis`: AI 设置现在通过 Web UI 中的“AI 配置”页面进行配置。 
        *   `accounts_to_monitor`: 要监控的完整个人主页 URL 列表（例如 `https://truthsocial.com/@realDonaldTrump`）。此列表可以先留空，稍后通过 Web UI 添加。

1.  **启动主程序:**
    ```

3.  **访问 Web UI:** 打开浏览器并访问 `http://127.0.0.1:8085`。

4.  在 Web UI 中添加/删除需要监控的账户，或调整其他配置。

    *   **AI 配置页面**: 导航到“AI 配置”选项卡以配置 AI 设置。
    *   **消息推送页面**: 导航到“消息推送”选项卡以配置 Telegram 设置。
    *   **内容页面**: 导航到“内容”选项卡以查看历史帖子。
    *   **同步最近7天内容按钮**: 在“内容”页面上，点击“同步最近7天内容”以获取更多历史数据。
    *   **同步最近一条按钮**: 在“内容”页面上，点击“同步最近一条”以获取所有监控账户的最新一条帖子。
