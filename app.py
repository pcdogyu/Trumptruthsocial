# d:\Trumptruthsocial\app.py
from flask import Flask, jsonify, render_template, request
import db_handler
import telegram_forwarder
# 假设您的爬虫和同步逻辑在其他模块中
# import scraper_controller 

app = Flask(__name__)

# --- 这是您现有的内容展示页面路由 ---
@app.route('/content')
@app.route('/content/<username>')
def content(username=None):
    # ... 您从数据库获取帖子列表的逻辑 ...
    # posts = db_handler.get_all_posts(username=username)
    # usernames = db_handler.get_all_usernames()
    # return render_template('content.html', posts=posts, usernames=usernames, selected_username=username)
    return "此部分为您现有代码，此处仅为占位"

# --- 新增：处理删除帖子的API ---
@app.route('/delete_post/<string:post_id>', methods=['POST'])
def delete_post_route(post_id):
    try:
        if db_handler.delete_post_by_id(post_id):
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

# ... 您现有的其他路由，如 sync_content, sync_latest_post 等 ...

if __name__ == '__main__':
    app.run(host='127.0.0.1', port=8085, debug=True)