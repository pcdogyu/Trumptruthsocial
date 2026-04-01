from flask import Flask, render_template, request, redirect, url_for, flash, jsonify, g
import yaml
import os
import logging
import threading
import time
import requests
import subprocess
from datetime import datetime

# 假设 database 模块已存在
import database

# 假设 main 模块中的 monitor_worker 和 load_config 函数
# 为了避免循环导入，这里不直接导入 main.py，而是模拟其功能或从 config.yaml 读取
# 实际项目中，可能需要将 monitor_worker 封装到单独的服务层，或者通过队列/事件机制通信

app = Flask(__name__)
app.secret_key = 'supersecretkey' # 生产环境中应使用更安全的密钥

CONFIG_FILE = 'config.yaml'
TEMPLATES_DIR = 'templates'

# 全局变量，用于存储配置和监控状态
# 注意：在多线程/多进程环境中，直接修改全局变量需要加锁
# 这里为了简化，暂时不加锁，但实际生产环境需要考虑并发问题
# 为了确保线程安全，我们引入锁来保护全局配置和同步状态。
global_config = {}
config_last_modified = 0
config_lock = threading.Lock() # 用于保护 global_config 和 config_last_modified

# --- 日志配置 ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

def load_config_from_file(path=CONFIG_FILE):
    """从文件加载 YAML 配置文件"""
    try:
        with open(path, 'r', encoding='utf-8') as f:
            return yaml.safe_load(f)
    except FileNotFoundError:
        logging.error(f"错误：配置文件 '{path}' 未找到。")
        return {}
    except yaml.YAMLError as e:
        logging.error(f"错误：解析配置文件 '{path}' 时出错: {e}")
        return {}

def get_current_config():
    """获取最新的配置，如果文件有更新则重新加载"""
    global global_config, config_last_modified, config_lock
    with config_lock: # 保护对 global_config 和 config_last_modified 的访问
        try:
            current_modified = os.path.getmtime(CONFIG_FILE)
            if current_modified > config_last_modified:
                logging.info("config.yaml 已更新，重新加载配置。")
                global_config = load_config_from_file(CONFIG_FILE)
                config_last_modified = current_modified
        except FileNotFoundError:
            logging.warning(f"config.yaml 未找到，使用空配置。")
            global_config = {}
        except Exception as e:
            logging.error(f"检查或加载 config.yaml 时出错: {e}")
            global_config = {} # 确保在出错时 global_config 仍然是一个字典
    return global_config

def mask_api_key(key):
    """遮蔽 API Key，仅显示开头和结尾的四位字符"""
    if not key or len(key) <= 8:
        return key
    return key[:4] + '*' * (len(key) - 8) + key[-4:]

def save_config(config_data, path=CONFIG_FILE):
    """保存配置到 YAML 文件"""
    try:
        with open(path, 'w', encoding='utf-8') as f:
            yaml.safe_dump(config_data, f, default_flow_style=False, allow_unicode=True)
        global config_last_modified, config_lock
        with config_lock: # 保护对 config_last_modified 的更新
            config_last_modified = os.path.getmtime(CONFIG_FILE) # 更新修改时间
        logging.info(f"配置已保存到 '{path}'。")
        return True
    except Exception as e:
        logging.error(f"保存配置到 '{path}' 时出错: {e}")
        return False

def get_git_commit_info():
    """获取 git 提交信息。"""
    try:
        commit_time_unix = subprocess.check_output(['git', 'log', '-1', '--format=%ct']).strip().decode('utf-8')
        commit_time = datetime.fromtimestamp(int(commit_time_unix)).strftime('%Y-%m-%d %H:%M:%S')
        commit_hash = subprocess.check_output(['git', 'rev-parse', '--short=8', 'HEAD']).strip().decode('utf-8')
        commit_branch = subprocess.check_output(['git', 'rev-parse', '--abbrev-ref', 'HEAD']).strip().decode('utf-8')
        return {
            'time': commit_time,
            'hash': commit_hash,
            'branch': commit_branch
        }
    except (subprocess.CalledProcessError, FileNotFoundError):
        logging.warning("无法获取 git 提交信息。是否已安装 git 并且这是一个 git 仓库？")
        return { 'time': 'N/A', 'hash': 'N/A', 'branch': 'N/A' }

@app.context_processor
def inject_git_info():
    """将 git 提交信息注入到所有模板中。"""
    if 'git_commit_info' not in g:
        g.git_commit_info = get_git_commit_info()
    return dict(git_commit_info=g.git_commit_info)

def create_templates_if_not_exists():
    """创建必要的模板文件，如果它们不存在的话"""
    if not os.path.exists(TEMPLATES_DIR):
        os.makedirs(TEMPLATES_DIR)

    # layout.html
    layout_path = os.path.join(TEMPLATES_DIR, 'layout.html')
    if not os.path.exists(layout_path):
        with open(layout_path, 'w', encoding='utf-8') as f:
            f.write("""
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{% block title %}TruthSocial Monitor{% endblock %}</title>
    <link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.5.2/css/bootstrap.min.css">
    <style>
        body { padding-top: 70px; }
        .footer {
            position: fixed;
            bottom: 0;
            width: 100%;
            height: 60px; /* Set the fixed height of the footer here */
            line-height: 60px; /* Vertically center the text there */
            background-color: #f5f5f5;
            text-align: center;
        }
        .post-card {
            margin-bottom: 20px;
            border: 1px solid #e0e0e0;
            border-radius: 5px;
            box-shadow: 0 2px 5px rgba(0,0,0,0.05);
        }
        .post-card .card-header {
            background-color: #f8f9fa;
            font-weight: bold;
        }
        .post-card .card-body {
            padding: 15px;
        }
        .post-card .card-footer {
            background-color: #f8f9fa;
            font-size: 0.85em;
            color: #6c757d;
        }
        .post-card .card-text {
            white-space: pre-wrap; /* Preserve whitespace and line breaks */
            word-wrap: break-word; /* Break long words */
        }
        .video-container {
            position: relative;
            padding-bottom: 56.25%; /* 16:9 aspect ratio */
            height: 0;
            overflow: hidden;
            max-width: 100%;
            background: #000;
            margin-top: 10px;
        }
        .video-container iframe,
        .video-container video {
            position: absolute;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
        }
    </style>
</head>
<body>
    <nav class="navbar navbar-expand-md navbar-dark bg-dark fixed-top">
        <a class="navbar-brand" href="{{ url_for('index') }}">TruthSocial Monitor</a>
        <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarNav" aria-controls="navbarNav" aria-expanded="false" aria-label="Toggle navigation">
            <span class="navbar-toggler-icon"></span>
        </button>
        <div class="collapse navbar-collapse" id="navbarNav">
            <ul class="navbar-nav mr-auto">
                <li class="nav-item {% if request.endpoint == 'index' %}active{% endif %}">
                    <a class="nav-link" href="{{ url_for('index') }}">配置</a>
                </li>
                <li class="nav-item {% if request.endpoint == 'ai_config' %}active{% endif %}">
                    <a class="nav-link" href="{{ url_for('ai_config') }}">AI 配置</a>
                </li>
                <li class="nav-item {% if request.endpoint == 'content' %}active{% endif %}">
                    <a class="nav-link" href="{{ url_for('content') }}">内容</a>
                </li>
            </ul>
        </div>
    </nav>

    <main role="main" class="container">
        {% with messages = get_flashed_messages(with_categories=true) %}
            {% if messages %}
                {% for category, message in messages %}
                    <div class="alert alert-{{ category }} alert-dismissible fade show" role="alert">
                        {{ message }}
                        <button type="button" class="close" data-dismiss="alert" aria-label="Close">
                            <span aria-hidden="true">&times;</span>
                        </button>
                    </div>
                {% endfor %}
            {% endif %}
        {% endwith %}
        {% block content %}{% endblock %}
    </main>

    <footer class="footer">
        <div class="container">
            <span class="text-muted">Code by Yuhao@jiansutech.com - {{ git_commit_info.time }} - {{ git_commit_info.hash }} - {{ git_commit_info.branch }}</span>
        </div>
    </footer>

    <script src="https://code.jquery.com/jquery-3.5.1.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/popper.js@1.16.1/dist/umd/popper.min.js"></script>
    <script src="https://stackpath.bootstrapcdn.com/bootstrap/4.5.2/js/bootstrap.min.js"></script>
    {% block scripts %}{% endblock %}
</body>
</html>
""")
        logging.info(f"Created {layout_path}")

    # index.html
    index_path = os.path.join(TEMPLATES_DIR, 'index.html')
    if not os.path.exists(index_path):
        with open(index_path, 'w', encoding='utf-8') as f:
            f.write("""
{% extends "layout.html" %}

{% block title %}配置{% endblock %}

{% block content %}
<h1 class="mt-5">配置</h1>

<form method="POST" action="{{ url_for('save_config_route') }}">
    <!-- Truth Social 认证设置 -->
    <div class="form-group">
        <label for="bearer_token">Bearer Token:</label>
        <input type="text" class="form-control" id="bearer_token" name="bearer_token" value="{{ masked_bearer_token }}">
        <small class="form-text text-muted">从 `get_token.py` 获取的认证令牌。</small>
    </div>
    <div class="form-group">
        <label for="truthsocial_username">Truth Social 用户名:</label>
        <input type="text" class="form-control" id="truthsocial_username" name="truthsocial_username" value="{{ config.auth.truthsocial_username if config.auth else '' }}">
        <small class="form-text text-muted">您在 Truth Social 上的用户名，例如 'pcdogyuhao'。</small>
    </div>
    <div class="form-group">
        <label for="refresh_interval">刷新间隔:</label>
        <input type="text" class="form-control" id="refresh_interval" name="refresh_interval" value="{{ config.refresh_interval if config.refresh_interval else '5m' }}">
        <small class="form-text text-muted">例如: 5m (5分钟), 1h (1小时)。</small>
    </div>

    <div class="form-group">
        <label for="accounts_to_monitor">监控账户 (每行一个用户名):</label>
        <textarea class="form-control" id="accounts_to_monitor" name="accounts_to_monitor" rows="5">{{ "\n".join(config.accounts_to_monitor) if config.accounts_to_monitor else '' }}</textarea>
        <small class="form-text text-muted">要监控的 Truth Social 用户名，每行一个。</small>
    </div>

    <button type="submit" class="btn btn-primary">保存配置</button>
</form>
""")
        logging.info(f"Created {index_path}")

    # content.html (New template for this request)
    content_path = os.path.join(TEMPLATES_DIR, 'content.html')
    if not os.path.exists(content_path):
        with open(content_path, 'w', encoding='utf-8') as f:
            f.write("""
{% extends "layout.html" %}

{% block title %}历史内容{% endblock %}

{% block content %}
<h1 class="mt-5">历史内容</h1>

<div class="mb-3">
    <button id="syncHistoricalButton" class="btn btn-info">同步最近7天内容</button>
    <button id="syncLatestButton" class="btn btn-primary ml-2">同步最近一条</button>
    <small class="form-text text-muted">点击同步按钮将尝试获取所有监控账户最近的帖子。</small>
</div>

<div class="row">
    <div class="col-md-3">
        <div class="list-group">
            <a href="{{ url_for('content') }}" class="list-group-item list-group-item-action {% if not selected_username %}active{% endif %}">所有账户</a>
            {% for user in usernames %}
            <a href="{{ url_for('content', username=user) }}" class="list-group-item list-group-item-action {% if selected_username == user %}active{% endif %}">{{ user }}</a>
            {% endfor %}
        </div>
    </div>
    <div class="col-md-9">
        {% if posts %}
            {% for post in posts %}
            <div class="card post-card">
                <div class="card-header">
                    <a href="https://truthsocial.com/@{{ post.username }}" target="_blank">@{{ post.username }}</a>
                    <span class="float-right text-muted">{{ post.timestamp }}</span>
                </div>
                <div class="card-body">
                    <p class="card-text">{{ post.content }}</p>
                    {% if post.video_url %}
                        <div class="video-container">
                            <video controls preload="metadata" style="width: 100%; height: auto;">
                                <source src="{{ post.video_url }}" type="video/mp4">
                                Your browser does not support the video tag.
                            </video>
                        </div>
                    {% endif %}
                    <a href="{{ post.web_url }}" target="_blank" class="btn btn-sm btn-outline-primary mt-2">查看原文</a>
                </div>
                <div class="card-footer">
                    Post ID: {{ post.id }}
                </div>
            </div>
            {% endfor %}
        {% else %}
            <p>没有找到历史帖子。</p>
        {% endif %}
    </div>
</div>
{% endblock %}

{% block scripts %}
<script>
    $(document).ready(function() {
        $('#syncHistoricalButton').on('click', function() {
            $(this).prop('disabled', true).text('同步中...');
            $.ajax({
                url: '{{ url_for("sync_content") }}',
                type: 'POST',
                success: function(response) {
                    alert(response.message);
                    $('#syncButton').prop('disabled', false).text('同步最近7天内容');
                    if (response.status === 'success') {
                        location.reload(); // 同步成功后刷新页面
                    }
                },
                error: function(xhr, status, error) {
                    alert('同步失败: ' + xhr.responseText);
                    $('#syncButton').prop('disabled', false).text('同步最近7天内容');
                }
            });
        });

        $('#syncLatestButton').on('click', function() {
            $(this).prop('disabled', true).text('同步中...');
            $.ajax({
                url: '{{ url_for("sync_latest_post") }}',
                type: 'POST',
                success: function(response) {
                    alert(response.message);
                    $('#syncLatestButton').prop('disabled', false).text('同步最近一条');
                    if (response.status === 'success') {
                        location.reload(); // 同步成功后刷新页面
                    }
                },
                error: function(xhr, status, error) {
                    alert('同步失败: ' + xhr.responseText);
                    $('#syncLatestButton').prop('disabled', false).text('同步最近一条');
                }
            });
        });
    });
</script>
{% endblock %}
""")
        logging.info(f"Created {content_path}")

    # message_push.html
    message_push_path = os.path.join(TEMPLATES_DIR, 'message_push.html')
    if not os.path.exists(message_push_path):
        with open(message_push_path, 'w', encoding='utf-8') as f:
            f.write("""
{% extends "layout.html" %}

{% block title %}消息推送配置{% endblock %}

{% block content %}
<h1 class="mt-5">消息推送配置</h1>

<form method="POST" action="{{ url_for('save_message_push_config_route') }}">
    <div class="form-group">
        <label for="bot_token">Telegram Bot Token:</label>
        <input type="text" class="form-control" id="bot_token" name="bot_token" value="{{ masked_bot_token }}">
        <small class="form-text text-muted">从 BotFather 获取的 Telegram 机器人令牌。</small>
    </div>

    <div class="form-group">
        <label for="chat_id">Telegram Chat ID:</label>
        <input type="text" class="form-control" id="chat_id" name="chat_id" value="{{ config.telegram.chat_id if config.telegram else '' }}">
        <small class="form-text text-muted">接收通知的 Telegram 聊天 ID。</small>
    </div>

    <button type="submit" class="btn btn-primary">保存推送配置</button>
    <button type="button" class="btn btn-secondary ml-2" id="testMessageButton">消息测试</button>
</form>
{% endblock %}

{% block scripts %}
<script>
    $(document).ready(function() {
        $('#testMessageButton').on('click', function() {
            var $button = $(this);
            var originalText = $button.text();
            $button.prop('disabled', true).text('测试中...');

            $.ajax({
                url: '{{ url_for("test_telegram_message") }}',
                type: 'POST',
                contentType: 'application/json',
                data: JSON.stringify({
                    bot_token: $('#bot_token').val(),
                    chat_id: $('#chat_id').val()
                }),
                success: function(response) {
                    alert(response.message);
                },
                error: function(xhr, status, error) {
                    var errorMessage = '测试失败';
                    try {
                        var errorJson = JSON.parse(xhr.responseText);
                        errorMessage += ': ' + errorJson.message;
                    } catch (e) {
                        errorMessage += ': ' + xhr.responseText;
                    }
                    alert(errorMessage);
                },
                complete: function() {
                    $button.prop('disabled', false).text(originalText);
                }
            });
        });
    });
</script>
{% endblock %}
""")
        logging.info(f"Created {message_push_path}")

# Flask Routes
@app.route('/')
def index():
    config = get_current_config()
    ai_api_key = config.get('ai_analysis', {}).get('api_key', '')
    masked_ai_api_key = mask_api_key(ai_api_key)
    
    bearer_token = config.get('auth', {}).get('bearer_token', '')
    masked_bearer_token = mask_api_key(bearer_token)
    telegram_bot_token = config.get('telegram', {}).get('bot_token', '')
    masked_bot_token = mask_api_key(telegram_bot_token)
    return render_template('index.html', config=config, masked_ai_api_key=masked_ai_api_key,
                           masked_bearer_token=masked_bearer_token, masked_bot_token=masked_bot_token)

@app.route('/save_config', methods=['POST'])
def save_config_route():
    new_config = get_current_config() # 获取当前配置作为基础
    
    # 更新 auth
    if 'auth' not in new_config:
        new_config['auth'] = {}
    
    # 更新 truthsocial_username
    new_config['auth']['truthsocial_username'] = request.form.get('truthsocial_username', '').strip()

    # 更新 bearer_token
    submitted_bearer_token = request.form.get('bearer_token', '').strip()
    if '*' not in submitted_bearer_token: # 如果提交的 Token 不包含星号，说明用户修改了
        new_config['auth']['bearer_token'] = submitted_bearer_token
    elif not submitted_bearer_token: # 如果用户提交的是空字符串
        new_config['auth']['bearer_token'] = ''
    # 否则，保持原有的 Token 不变
    
    # 移除 Telegram 相关配置，因为它们已移至 /message_push 页面
    # 移除 AI 相关配置，因为它们已移至 /ai_config 页面
    # new_config['telegram']['chat_id'] = request.form.get('chat_id', '').strip() # 这行也应该移除

    # 更新 refresh_interval
    new_config['refresh_interval'] = request.form.get('refresh_interval', '5m').strip()

    # 更新 accounts_to_monitor
    accounts_str = request.form.get('accounts_to_monitor', '').strip()
    new_config['accounts_to_monitor'] = [acc.strip() for acc in accounts_str.split('\n') if acc.strip()]

    if save_config(new_config):
        flash('配置已成功保存！', 'success')
    else:
        flash('保存配置失败！', 'danger')
    return redirect(url_for('index'))

@app.route('/content')
def content():
    selected_username = request.args.get('username')
    if selected_username:
        posts = database.get_all_posts(username=selected_username, limit=50) # 限制显示数量
    else:
        posts = database.get_all_posts(limit=50) # 限制显示数量
    
    usernames = database.get_unique_usernames()
    return render_template('content.html', posts=posts, usernames=usernames, selected_username=selected_username)

@app.route('/message_push')
def message_push():
    config = get_current_config()
    telegram_bot_token = config.get('telegram', {}).get('bot_token', '')
    masked_bot_token = mask_api_key(telegram_bot_token)
    return render_template('message_push.html', config=config, masked_bot_token=masked_bot_token)

@app.route('/save_message_push_config', methods=['POST'])
def save_message_push_config_route():
    new_config = get_current_config()

    if 'telegram' not in new_config:
        new_config['telegram'] = {}
    
    submitted_bot_token = request.form.get('bot_token', '').strip()
    if '*' not in submitted_bot_token:
        new_config['telegram']['bot_token'] = submitted_bot_token
    elif not submitted_bot_token:
        new_config['telegram']['bot_token'] = ''
    
    new_config['telegram']['chat_id'] = request.form.get('chat_id', '').strip()

    if save_config(new_config):
        flash('消息推送配置已成功保存！', 'success')
    else:
        flash('保存消息推送配置失败！', 'danger')
    return redirect(url_for('message_push'))

def _send_telegram_message_internal(bot_token, chat_id, text):
    """
    内部辅助函数，用于发送 Telegram 消息。
    """
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
        if result.get('ok'):
            logging.info(f"Test Telegram message sent successfully to Chat ID: {chat_id}")
            return True, "测试消息发送成功！"
        else:
            logging.error(f"Telegram API error (test message): {result.get('description')}")
            return False, f"Telegram API 错误: {result.get('description')}"
    except requests.exceptions.RequestException as e:
        logging.error(f"发送 Telegram 测试消息时网络错误: {e}")
        return False, f"网络错误: {e}"

@app.route('/test_telegram_message', methods=['POST'])
def test_telegram_message():
    request_data = request.get_json()
    submitted_bot_token = request_data.get('bot_token', '').strip()
    submitted_chat_id = request_data.get('chat_id', '').strip()

    if not submitted_chat_id:
        return jsonify({'status': 'error', 'message': 'Chat ID 不能为空。'}), 400

    # 如果提交的 token 包含星号，说明用户未修改，应使用已保存的真实 token
    if '*' in submitted_bot_token:
        current_config = get_current_config()
        bot_token_to_use = current_config.get('telegram', {}).get('bot_token')
    else:
        bot_token_to_use = submitted_bot_token

    if not bot_token_to_use:
        return jsonify({'status': 'error', 'message': 'Bot Token 未配置或无效。请先保存配置。'}), 400

    test_message_text = "这是一个来自 TruthSocial Monitor 的测试消息！"
    success, message = _send_telegram_message_internal(bot_token_to_use, submitted_chat_id, test_message_text)

    if success:
        return jsonify({'status': 'success', 'message': message}), 200
    else:
        return jsonify({'status': 'error', 'message': message}), 500

@app.route('/ai_config')
def ai_config():
    config = get_current_config()
    ai_api_key = config.get('ai_analysis', {}).get('api_key', '')
    masked_ai_api_key = mask_api_key(ai_api_key)
    return render_template('ai_config.html', config=config, masked_ai_api_key=masked_ai_api_key)

@app.route('/save_ai_config', methods=['POST'])
def save_ai_config_route():
    new_config = get_current_config()

    if 'ai_analysis' not in new_config:
        new_config['ai_analysis'] = {}
    
    new_config['ai_analysis']['enabled'] = 'ai_enabled' in request.form 
    submitted_ai_api_key = request.form.get('ai_api_key', '').strip()
    if '*' not in submitted_ai_api_key:
        new_config['ai_analysis']['api_key'] = submitted_ai_api_key
    elif not submitted_ai_api_key:
        new_config['ai_analysis']['api_key'] = ''
    new_config['ai_analysis']['prompt'] = request.form.get('ai_prompt', '').strip()

    if save_config(new_config):
        flash('AI 配置已成功保存！', 'success')
    else:
        flash('保存 AI 配置失败！', 'danger')
    return redirect(url_for('ai_config'))

# 全局变量，用于控制同步线程
# 同样需要锁来保护 sync_in_progress
sync_thread = None
sync_in_progress = False
sync_lock = threading.Lock() # 用于保护 sync_in_progress

def _sync_worker(app_context, monitor_instance, accounts_to_sync, days_to_sync=7):
    """
    后台同步工作函数。
    注意：这个函数需要一个 TruthSocialMonitor 实例，并且能够获取到配置。
    为了避免循环导入，这里假设 monitor_instance 已经传入。
    """
    global sync_in_progress, sync_lock
    with app.app_context(): # 直接使用 app.app_context()
        try:
            logging.info(f"开始同步最近 {days_to_sync} 天的内容...")
            with sync_lock:
                sync_in_progress = True
            
            sync_successful = True
            for username in accounts_to_sync:
                logging.info(f"正在使用 Selenium 抓取 @{username} 最近 {days_to_sync} 天的历史帖子。")
                try:
                    posts = monitor_instance.fetch_historical_posts_selenium(username, days_to_sync)
                    for post in posts:
                        if database.add_post(post):
                            logging.info(f"已同步并添加新帖子: {post.get('id')} by @{username}")
                    time.sleep(1)
                except Exception as e:
                    logging.error(f"同步用户 @{username} 的历史内容时出错: {e}")
                    sync_successful = False
            
            if sync_successful:
                logging.info(f"最近 {days_to_sync} 天内容同步完成。")
                flash('内容同步成功！', 'success')
            else:
                flash('部分内容同步失败，请查看日志获取详情。', 'warning')

        except Exception as e:
            logging.error(f"同步历史内容时发生全局错误: {e}")
            flash(f'内容同步失败: {e}', 'danger')
        finally:
            with sync_lock:
                sync_in_progress = False

@app.route('/sync_content', methods=['POST'])
def sync_content():
    global sync_thread, sync_in_progress, sync_lock
    with sync_lock: # 保护对 sync_in_progress 的检查和后续操作
        if sync_in_progress:
            return jsonify({'status': 'info', 'message': '同步操作正在进行中，请稍候。'}), 202

    config = get_current_config()
    accounts_to_monitor = config.get('accounts_to_monitor', [])
    if not accounts_to_monitor:
        return jsonify({'status': 'error', 'message': '没有配置要监控的账户，无法同步。'}), 400

    # 导入 TruthSocialMonitor 类
    # 避免循环导入，这里假设 monitor.py 可以在需要时被导入
    # 或者，更好的做法是将 monitor_worker 封装成一个可调用的函数，并传入必要的依赖
    try:
        from monitor import TruthSocialMonitor
        monitor_instance = TruthSocialMonitor(config)
    except Exception as e:
        logging.error(f"无法创建 TruthSocialMonitor 实例: {e}")
        return jsonify({'status': 'error', 'message': f'无法初始化监控器: {e}'}), 500

    # 在新线程中启动同步任务
    # 不再传递 app_context，线程函数内部会自行获取
    sync_thread = threading.Thread(target=_sync_worker, args=(monitor_instance, accounts_to_monitor, 7))
    sync_thread.daemon = True # 设置为守护线程，主程序退出时自动终止
    sync_thread.start()

    return jsonify({'status': 'success', 'message': '同步任务已在后台启动。请稍后刷新页面查看结果。'}), 200

# 全局变量，用于控制“同步最近一条”线程
sync_latest_thread = None
sync_latest_in_progress = False
sync_latest_lock = threading.Lock() # 用于保护 sync_latest_in_progress

def _sync_latest_worker(app_context, monitor_instance, accounts_to_sync):
    """
    后台同步最近一条帖子工作函数。
    """
    global sync_latest_in_progress, sync_latest_lock
    with app.app_context(): # 直接使用 app.app_context()
        try:
            logging.info("开始同步所有监控账户的最近一条帖子...")
            with sync_latest_lock:
                sync_latest_in_progress = True
            
            sync_successful = True
            for username in accounts_to_sync:
                logging.info(f"正在同步用户 @{username} 的最近一条帖子...")
                try:
                    # 调用 fetch_latest_posts，它使用 requests 快速获取最新帖子
                    posts = monitor_instance.fetch_latest_posts(username)
                    if posts:
                        # 遍历所有获取到的帖子，并添加到数据库（add_post会处理去重）
                        for post in posts:
                            if database.add_post(post):
                                logging.info(f"已同步并添加新帖子: {post.get('id')} by @{username}")
                        time.sleep(0.5) # 短暂延时
                    else:
                        logging.info(f"未获取到用户 @{username} 的最新帖子。")
                except Exception as e:
                    logging.error(f"同步用户 @{username} 的最近一条帖子时发生错误: {e}")
                    sync_successful = False
            
            if sync_successful:
                logging.info("所有监控账户的最近一条帖子同步完成。")
                flash('最近一条帖子同步成功！', 'success')
            else:
                flash('部分最近一条帖子同步失败，请查看日志获取详情。', 'warning')

        except Exception as e:
            logging.error(f"同步最近一条帖子时发生全局错误: {e}")
            flash(f'同步最近一条帖子失败: {e}', 'danger')
        finally:
            with sync_latest_lock:
                sync_latest_in_progress = False

@app.route('/sync_latest_post', methods=['POST'])
def sync_latest_post():
    global sync_latest_thread, sync_latest_in_progress, sync_latest_lock
    with sync_latest_lock:
        if sync_latest_in_progress:
            return jsonify({'status': 'info', 'message': '同步最近一条帖子操作正在进行中，请稍候。'}), 202

    config = get_current_config()
    accounts_to_monitor = config.get('accounts_to_monitor', [])
    if not accounts_to_monitor:
        return jsonify({'status': 'error', 'message': '没有配置要监控的账户，无法同步最近一条帖子。'}), 400

    try:
        from monitor import TruthSocialMonitor
        monitor_instance = TruthSocialMonitor(config)
    except Exception as e:
        logging.error(f"无法创建 TruthSocialMonitor 实例: {e}")
        return jsonify({'status': 'error', 'message': f'无法初始化监控器: {e}'}), 500

    app_context = app.app_context()
    sync_latest_thread = threading.Thread(target=_sync_latest_worker, args=(monitor_instance, accounts_to_monitor)) # 不再传递 app_context
    sync_latest_thread.daemon = True
    sync_latest_thread.start()

    return jsonify({'status': 'success', 'message': '同步最近一条帖子任务已在后台启动。请稍后刷新页面查看结果。'}), 200