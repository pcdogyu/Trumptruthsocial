# d:\Trumptruthsocial\app.py
from flask import Flask, jsonify, render_template, request, redirect, url_for
import database as db_handler # 统一使用 database.py 并设置别名
import telegram_forwarder
import config_manager # 导入新的配置管理器
import os
import subprocess
import logging # 确保 logging 模块已导入
# 假设您的爬虫和同步逻辑在其他模块中
# import scraper_controller 

app = Flask(__name__)

def create_templates_if_not_exists():
    """检查并创建 templates 文件夹和基础的 layout.html 文件"""
    templates_dir = 'templates'
    if not os.path.exists(templates_dir):
        os.makedirs(templates_dir)
        logging.info(f"Created directory: {templates_dir}")

    layout_path = os.path.join(templates_dir, 'layout.html')
    if not os.path.exists(layout_path):
        # 根据 content.html, 模板需要 Bootstrap 和 jQuery
        layout_content = """<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
    <link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.5.2/css/bootstrap.min.css">
    <title>{% block title %}{% endblock %}</title>
</head>
<body>
    <div class="container">
        {% block content %}{% endblock %}
    </div>
    <script src="https://code.jquery.com/jquery-3.5.1.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/@popperjs/core@2.9.2/dist/umd/popper.min.js"></script>
    <script src="https://stackpath.bootstrapcdn.com/bootstrap/4.5.2/js/bootstrap.min.js"></script>
    {% block scripts %}{% endblock %}
</body>
</html>"""
        with open(layout_path, 'w', encoding='utf-8') as f:
            f.write(layout_content)
        logging.info(f"Created file: {layout_path}")


def get_git_commit_info():
    """尝试获取当前的 Git 提交信息。"""
    git_info = {
        'time': 'N/A',
        'hash': 'N/A',
        'branch': 'N/A'
    }
    try:
        # 获取最新提交时间
        git_info['time'] = subprocess.check_output(['git', 'log', '-1', '--format=%cd', '--date=format:%Y-%m-%d %H:%M:%S']).decode('utf-8').strip()
        # 获取最新提交哈希
        git_info['hash'] = subprocess.check_output(['git', 'rev-parse', '--short', 'HEAD']).decode('utf-8').strip()
        # 获取当前分支名
        git_info['branch'] = subprocess.check_output(['git', 'rev-parse', '--abbrev-ref', 'HEAD']).decode('utf-8').strip()
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        logging.warning(f"无法获取 Git 提交信息: {e}. 可能不在 Git 仓库中或未安装 Git。")
        # 如果无法获取，保持为 N/A
    except Exception as e:
        logging.error(f"获取 Git 提交信息时发生未知错误: {e}")
    return git_info


# --- 新增：根路径重定向到 /content ---
@app.route('/')
def index():
    return redirect(url_for('content'))

# --- 这是您现有的内容展示页面路由 ---
@app.route('/content')
@app.route('/content/<username>')
def content(username=None):
    posts = db_handler.get_all_posts(username=username)
    usernames = db_handler.get_unique_usernames()
    git_commit_info = get_git_commit_info() # 获取 Git 信息
    return render_template('content.html', posts=posts, usernames=usernames, selected_username=username, git_commit_info=git_commit_info)

# --- 新增：处理删除帖子的API ---
@app.route('/delete_post/<string:post_id>', methods=['POST'])
def delete_post_route(post_id):
    try:
        if db_handler.delete_post(post_id):
            return jsonify({'status': 'success', 'message': f'帖子 {post_id} 已删除。'})
        else:
            return jsonify({'message': f'未在数据库中找到帖子 {post_id}。'}), 404
    except Exception as e:
        app.logger.error(f"删除帖子 {post_id} 时出错: {e}")
        return jsonify({'message': '服务器内部错误。'}), 500

# --- 新增：处理转发帖子的API ---
@app.route('/forward_post/<string:post_id>', methods=['POST'])
def forward_post_route(post_id):
    post = db_handler.get_post_by_id(post_id)
    if not post:
        return jsonify({'message': '帖子未找到。'}), 404
    
    success, message = telegram_forwarder.forward_post(post)
    
    if success:
        return jsonify({'status': 'success', 'message': message})
    else:
        return jsonify({'message': message}), 500

# --- 为UI中的按钮和链接提供占位路由以修复BuildError ---
@app.route('/sync_content', methods=['POST'])
def sync_content():
    # 历史同步的后端逻辑将在此实现
    # 目前返回一个提示信息
    return jsonify({'status': 'info', 'message': '历史同步任务已启动，此功能正在开发中。'})

@app.route('/sync_latest_post', methods=['POST'])
def sync_latest_post():
    # 最新帖子同步的后端逻辑将在此实现
    # 目前返回一个提示信息
    return jsonify({'status': 'info', 'message': '最新帖子同步任务已启动，此功能正在开发中。', 'new_posts': 0})

@app.route('/ai_config')
def ai_config():
    # 为AI配置页面提供占位符，修复模板中的链接错误
    return "<h1>AI 配置页面</h1><p>此页面正在建设中。</p>"

@app.route('/message_push')
def message_push():
    """显示和编辑 Telegram 消息推送配置页面。"""
    config = config_manager.load_config()
    telegram_config = config.get('telegram', {})
    bot_token = telegram_config.get('bot_token', '')
    chat_id = telegram_config.get('chat_id', '')
    git_commit_info = get_git_commit_info() # 获取 Git 信息
    return render_template('message_push.html', bot_token=bot_token, chat_id=chat_id, git_commit_info=git_commit_info)

@app.route('/message_push/save', methods=['POST'])
def message_push_save():
    """保存 Telegram 消息推送配置。"""
    bot_token = request.form.get('bot_token')
    chat_id = request.form.get('chat_id')

    if not bot_token or not chat_id:
        return jsonify({'status': 'error', 'message': 'Bot Token 和 Chat ID 不能为空。'}), 400

    config = config_manager.load_config()
    if 'telegram' not in config:
        config['telegram'] = {}
    config['telegram']['bot_token'] = bot_token
    config['telegram']['chat_id'] = chat_id

    config_manager.save_config(config)
    logging.info("Telegram 配置已更新。")
    return jsonify({'status': 'success', 'message': 'Telegram 配置已成功保存。'})


@app.route('/config_page')
def config_page():
    # 为通用配置页面提供占位符
    return "<h1>通用配置页面</h1><p>此页面正在建设中。</p>"

if __name__ == '__main__':
    app.run(host='127.0.0.1', port=8085, debug=True)