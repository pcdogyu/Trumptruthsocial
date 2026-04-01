import yaml
from flask import Flask, render_template, request, redirect, url_for, flash, send_from_directory
import os
import requests
import json
import database # 导入数据库模块

# 定义配置文件的绝对路径
CONFIG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'config.yaml')

app = Flask(__name__)
app.secret_key = os.urandom(24) # 用于 flash 消息的安全密钥

def send_telegram_message(bot_token, chat_id, text):
    """使用 HTML 格式发送消息到 Telegram，并返回结果"""
    api_url = f"https://api.telegram.org/bot{bot_token}/sendMessage"
    payload = {
        'chat_id': chat_id,
        'text': text,
        'parse_mode': 'HTML',
        'disable_web_page_preview': True
    }
    try:
        response = requests.post(api_url, data=payload, timeout=10)
        response.raise_for_status()
        result = response.json()
        return result.get('ok', False), result.get('description', 'Unknown error.')
    except requests.exceptions.RequestException as e:
        return False, str(e)

def load_config():
    """加载 YAML 配置文件"""
    try:
        with open(CONFIG_PATH, 'r', encoding='utf-8') as f:
            return yaml.safe_load(f)
    except FileNotFoundError:
        return {}

def save_config(config_data):
    """保存数据到 YAML 配置文件"""
    with open(CONFIG_PATH, 'w', encoding='utf-8') as f:
        yaml.dump(config_data, f, allow_unicode=True, sort_keys=False)

@app.route('/favicon.ico')
def favicon():
    return send_from_directory(os.path.join(app.root_path),
                               'favicon.ico', mimetype='image/vnd.microsoft.icon')

@app.route('/', methods=['GET', 'POST'])
def settings():
    config = load_config()
    # 定义一个特殊的占位符来表示未更改
    TOKEN_PLACEHOLDER_PREFIX = "********"

    if request.method == 'POST':
        # 更新监控账户列表
        accounts_str = request.form.get('accounts_to_monitor', '')
        accounts_list = [acc.strip() for acc in accounts_str.splitlines() if acc.strip()]
        config['accounts_to_monitor'] = sorted(list(set(accounts_list)))

        # 更新刷新间隔
        config['refresh_interval'] = request.form.get('refresh_interval', '5m')

        # 更新 Truth Social 认证设置
        if 'auth' not in config: config['auth'] = {}
        config['auth']['bearer_token'] = request.form.get('auth_bearer_token', '')

        # 更新 Telegram 设置
        if 'telegram' not in config: config['telegram'] = {}
        
        # 处理 Bot Token 的更新逻辑
        new_bot_token = request.form.get('telegram_bot_token', '').strip()
        # 只有当用户输入了新的、非占位符的 Token 时才更新
        if new_bot_token and not new_bot_token.startswith(TOKEN_PLACEHOLDER_PREFIX):
            config['telegram']['bot_token'] = new_bot_token
        # 如果用户提交的是空字符串，也更新（允许用户清空Token）
        elif not new_bot_token:
            config['telegram']['bot_token'] = ''

        # 对 chat_id 做一个简单的类型转换尝试
        chat_id_str = request.form.get('telegram_chat_id', '')
        try:
            config['telegram']['chat_id'] = int(chat_id_str)
        except (ValueError, TypeError):
            config['telegram']['chat_id'] = chat_id_str

        # 更新 AI 分析设置
        if 'ai_analysis' not in config: config['ai_analysis'] = {}
        config['ai_analysis']['enabled'] = 'ai_enabled' in request.form
        config['ai_analysis']['api_key'] = request.form.get('ai_api_key', '')
        config['ai_analysis']['prompt'] = request.form.get('ai_prompt', '')

        save_config(config)
        flash('配置已成功保存！后台脚本将在下一个周期加载新配置。', 'success')
        return redirect(url_for('settings'))

    # 为 GET 请求准备用于显示的数据
    accounts_text = "\n".join(config.get('accounts_to_monitor', []))
    
    # 创建一个用于显示的配置副本
    display_config = json.loads(json.dumps(config))

    # 遮蔽 Bot Token
    bot_token = display_config.get('telegram', {}).get('bot_token', '')
    if bot_token and len(bot_token) > 8:
        masked_token = f"{bot_token[:4]}...{bot_token[-4:]}"
        # 使用一个可识别的前缀，以便在 POST 时判断
        display_config['telegram']['bot_token'] = f"{TOKEN_PLACEHOLDER_PREFIX}{masked_token}"
    elif bot_token: # 如果token不够长，就全显示
        display_config['telegram']['bot_token'] = bot_token


    return render_template('index.html', config=display_config, accounts_text=accounts_text)

@app.route('/history')
def history():
    """显示历史帖子页面"""
    posts = database.get_recent_posts(limit=100) # 从数据库获取历史记录
    return render_template('history.html', posts=posts)

@app.route('/test-telegram', methods=['POST'])
def test_telegram():
    """发送 Telegram 测试消息"""
    config = load_config()
    bot_token = config.get('telegram', {}).get('bot_token')
    chat_id = config.get('telegram', {}).get('chat_id')

    if not bot_token or not chat_id or 'YOUR_TELEGRAM_BOT_TOKEN' in bot_token:
        flash('Telegram Bot Token 或 Chat ID 未在配置页面中正确设置。', 'danger')
        return redirect(url_for('settings'))

    test_message = "✅ 这是一条来自 TruthSocial Monitor Web UI 的测试消息。\n\n如果您收到此消息，说明您的 Telegram 配置正确！"
    success, description = send_telegram_message(bot_token, chat_id, test_message)

    if success:
        flash('测试消息已成功发送！请检查您的 Telegram。', 'success')
    else:
        flash(f'发送测试消息失败: {description}', 'danger')
    
    return redirect(url_for('settings'))

def create_templates_if_not_exists():
    """如果模板文件不存在，则创建它们"""
    templates_dir = 'templates'
    index_path = os.path.join(templates_dir, 'index.html')
    history_path = os.path.join(templates_dir, 'history.html')

    os.makedirs(templates_dir, exist_ok=True)

    if not os.path.exists(index_path):
        with open(index_path, 'w', encoding='utf-8') as f:
            f.write("""
<!DOCTYPE html>
<html lang="zh-cn">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>监控配置</title>
    <style>
        body { font-family: sans-serif; margin: 2em; background-color: #f4f4f9; color: #333; }
        .container { max-width: 700px; margin: auto; padding: 2em; background: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        h1, h2 { color: #333; }
        label { display: block; margin-bottom: 0.5em; font-weight: bold; }
        input[type="text"], textarea { width: 97%; padding: 10px; border: 1px solid #ccc; border-radius: 5px; font-size: 1em; }
        textarea { font-family: monospace; }
        .checkbox-label { display: flex; align-items: center; font-weight: normal; }
        .checkbox-label input { margin-right: 0.5em; }
        .save-btn { background: #007bff; color: white; border: none; padding: 12px 20px; border-radius: 5px; cursor: pointer; font-size: 1.1em; width: 100%; margin-top: 1.5em; }
        .save-btn:hover { background: #0056b3; }
        .test-btn { background: #17a2b8; color: white; border: none; padding: 10px 15px; border-radius: 5px; cursor: pointer; font-size: 1em; }
        .test-btn:hover { background: #138496; }
        .alert { padding: 1em; margin-bottom: 1em; border-radius: 5px; }
        .alert-success { background-color: #d4edda; color: #155724; border: 1px solid #c3e6cb; }
        .alert-danger { background-color: #f8d7da; color: #721c24; border: 1px solid #f5c6cb; }
        .nav-links { margin-bottom: 1.5em; font-size: 1.2em; border-bottom: 1px solid #eee; padding-bottom: 1em;}
        .nav-links a { text-decoration: none; color: #007bff; margin-right: 1em; }
        .nav-links a.active { font-weight: bold; }
        /* Tab styles */
        .tab { overflow: hidden; border: 1px solid #ccc; background-color: #f1f1f1; border-radius: 5px 5px 0 0; }
        .tab button { background-color: inherit; float: left; border: none; outline: none; cursor: pointer; padding: 14px 16px; transition: 0.3s; font-size: 0.95em; }
        .tab button:hover { background-color: #ddd; }
        .tab button.active { background-color: #ccc; }
        .tabcontent { display: none; padding: 20px 12px; border: 1px solid #ccc; border-top: none; border-radius: 0 0 5px 5px; animation: fadeEffect 0.5s; }
        @keyframes fadeEffect { from {opacity: 0;} to {opacity: 1;} }
        .form-section { margin-bottom: 1.5em; }
    </style>
</head>
<body>
    <div class="container">
        <h1>监控配置</h1>
        <div class="nav-links">
            <a href="{{ url_for('settings') }}" class="active">配置</a> | <a href="{{ url_for('history') }}">历史记录</a>
        </div>
        {% with messages = get_flashed_messages(with_categories=true) %}
          {% if messages %}
            {% for category, message in messages %}
              <div class="alert alert-{{ category }}">{{ message }}</div>
            {% endfor %}
          {% endif %}
        {% endwith %}

        <div class="tab">
            <button class="tablinks" onclick="openTab(event, 'Accounts')">监控账户</button>
            <button class="tablinks" onclick="openTab(event, 'Timing')">时间配置</button>
            <button class="tablinks" onclick="openTab(event, 'Auth')">Truth Social 认证</button>
            <button class="tablinks" onclick="openTab(event, 'AI')">AI 分析</button>
            <button class="tablinks" onclick="openTab(event, 'Notifications')">通知消息</button>
        </div>

        <form method="POST">
            <div id="Accounts" class="tabcontent">
                <div class="form-section">
                    <label for="accounts_to_monitor">每行一个账户名</label>
                    <textarea id="accounts_to_monitor" name="accounts_to_monitor" rows="5" placeholder="例如:&#10;realDonaldTrump&#10;another_user">{{ accounts_text }}</textarea>
                </div>
            </div>

            <div id="Timing" class="tabcontent">
                <div class="form-section">
                    <label for="refresh_interval">刷新间隔 (例如: 5m, 1h, 30s)</label>
                    <input type="text" id="refresh_interval" name="refresh_interval" value="{{ config.get('refresh_interval', '5m') }}">
                </div>
            </div>

            <div id="Auth" class="tabcontent">
                <div class="form-section">
                    <label for="auth_bearer_token">Bearer Token (可选)</label>
                    <input type="text" id="auth_bearer_token" name="auth_bearer_token" value="{{ config.get('auth', {}).get('bearer_token', '') }}">
                </div>
            </div>

            <div id="AI" class="tabcontent">
                <div class="form-section">
                    <label class="checkbox-label">
                        <input type="checkbox" name="ai_enabled" {% if config.get('ai_analysis', {}).get('enabled') %}checked{% endif %}>
                        启用 AI 分析
                    </label>
                </div>
                <div class="form-section">
                    <label for="ai_api_key">API Key</label>
                    <input type="text" id="ai_api_key" name="ai_api_key" value="{{ config.get('ai_analysis', {}).get('api_key', '') }}">
                </div>
                <div class="form-section">
                    <label for="ai_prompt">Prompt</label>
                    <textarea id="ai_prompt" name="ai_prompt" rows="3">{{ config.get('ai_analysis', {}).get('prompt', '') }}</textarea>
                </div>
            </div>

            <div id="Notifications" class="tabcontent">
                <div class="form-section">
                    <label for="telegram_bot_token">Bot Token</label>
                    <input type="text" id="telegram_bot_token" name="telegram_bot_token" value="{{ config.get('telegram', {}).get('bot_token', '') }}">
                </div>
                <div class="form-section">
                    <label for="telegram_chat_id">Chat ID</label>
                    <input type="text" id="telegram_chat_id" name="telegram_chat_id" value="{{ config.get('telegram', {}).get('chat_id', '') }}">
                </div>
                <div class="form-section" style="margin-top: 2em; padding-top: 2em; border-top: 1px solid #eee;">
                    <h2>测试 Telegram 推送</h2>
                    <p>点击下方按钮，将发送一条测试消息到您配置的 Chat ID。</p>
                    <!-- 这个按钮会提交一个隐藏的、独立的表单，以避免与主设置表单冲突 -->
                    <button type="button" class="test-btn" onclick="document.getElementById('test-telegram-form').submit();">发送测试消息</button>
                </div>
            </div>

            <button type="submit" class="save-btn">保存所有配置</button>
        </form>

        <!-- 用于测试按钮的隐藏表单 -->
        <form action="{{ url_for('test_telegram') }}" method="POST" id="test-telegram-form" style="display: none;"></form>
    </div>

    <script>
    function openTab(evt, tabName) {
        var i, tabcontent, tablinks;
        tabcontent = document.getElementsByClassName("tabcontent");
        for (i = 0; i < tabcontent.length; i++) {
            tabcontent[i].style.display = "none";
        }
        tablinks = document.getElementsByClassName("tablinks");
        for (i = 0; i < tablinks.length; i++) {
            tablinks[i].className = tablinks[i].className.replace(" active", "");
        }
        document.getElementById(tabName).style.display = "block";
        evt.currentTarget.className += " active";
    }
    // 默认打开第一个标签页
    document.addEventListener("DOMContentLoaded", function() {
        document.getElementsByClassName("tablinks")[0].click();
    });
    </script>
</body>
</html>
""")

    if not os.path.exists(history_path):
        with open(history_path, 'w', encoding='utf-8') as f:
            f.write("""
<!DOCTYPE html>
<html lang="zh-cn">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>历史记录</title>
    <style>
        body { font-family: sans-serif; margin: 2em; background-color: #f4f4f9; color: #333; }
        .container { max-width: 800px; margin: auto; padding: 2em; background: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        h1 { color: #333; }
        .nav-links { margin-bottom: 1.5em; font-size: 1.2em; border-bottom: 1px solid #eee; padding-bottom: 1em;}
        .nav-links a { text-decoration: none; color: #007bff; margin-right: 1em; }
        .nav-links a.active { font-weight: bold; }
        .post { border: 1px solid #ddd; border-radius: 5px; padding: 1em; margin-bottom: 1em; background-color: #fdfdfd; }
        .post-video { margin-bottom: 1em; }
        .post-header { font-weight: bold; margin-bottom: 0.5em; color: #555; }
        .post-content { white-space: pre-wrap; word-wrap: break-word; line-height: 1.6; }
        .post-footer { margin-top: 1em; font-size: 0.9em; text-align: right; }
        .post-footer a { text-decoration: none; color: #007bff; }
    </style>
</head>
<body>
    <div class="container">
        <h1>历史记录</h1>
        <div class="nav-links">
            <a href="{{ url_for('settings') }}">配置</a> | <a href="{{ url_for('history') }}" class="active">历史记录</a>
        </div>
        {% if posts %}
            {% for post in posts %}
            <div class="post">
                <div class="post-header">@{{ post.username }}</div>
                {% if post.video_url %}
                <div class="post-video">
                    <video controls preload="metadata" style="max-width: 100%; border-radius: 5px;">
                        <source src="{{ post.video_url }}" type="video/mp4">
                        您的浏览器不支持视频播放。
                    </video>
                </div>
                {% endif %}
                {% if post.content %}
                <div class="post-content">{{ post.content }}</div>
                {% endif %}
                <div class="post-footer"><a href="{{ post.web_url }}" target="_blank" rel="noopener noreferrer">查看原文 &rarr;</a></div>
            </div>
            {% endfor %}
        {% else %}
            <p>暂无历史记录。请等待后台脚本抓取到新帖子。</p>
        {% endif %}
    </div>
</body>
</html>
""")