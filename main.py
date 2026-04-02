import yaml
import time
import sys
import logging
import requests
import json
import os
import threading

# 导入您编写的监视器类
from monitor import TruthSocialMonitor # monitor 模块内部也需要更新为 config_manager
import database # 导入数据库模块
from app import app, create_templates_if_not_exists # 导入 Flask app 和模板创建函数

# --- 常量定义 ---
CONFIG_FILE = 'config.yaml'

# --- 日志配置 ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s') # 保持日志配置
import config_manager # 导入新的配置管理器

def parse_duration(interval_str):
    """将 '5m', '1h' 这样的字符串解析为秒数"""
    unit = interval_str[-1].lower()
    try:
        value = int(interval_str[:-1])
        if unit == 's':
            return value
        elif unit == 'm':
            return value * 60
        elif unit == 'h':
            return value * 3600
        else:
            logging.warning(f"未知的时间单位 '{unit}' in '{interval_str}'. 默认使用 300 秒。")
            return 300
    except (ValueError, IndexError):
        logging.warning(f"无法解析刷新间隔 '{interval_str}'. 默认使用 300 秒。")
        return 300

def send_telegram_video(bot_token, chat_id, video_url, caption):
    """使用 sendVideo API 发送视频到 Telegram"""
    api_url = f"https://api.telegram.org/bot{bot_token}/sendVideo"
    payload = {
        'chat_id': chat_id,
        'video': video_url,
        'caption': caption,
        'parse_mode': 'HTML'
    }
    for attempt in range(30):
        try:
            # 视频上传可能需要更长时间，增加超时
            response = requests.post(api_url, data=payload, timeout=60)
            response.raise_for_status()
            result = response.json()
            if result.get('ok'):
                logging.info(f"成功发送视频到 Chat ID: {chat_id}")
                return True
            else:
                logging.error(f"Telegram API 错误 (sendVideo) [第 {attempt+1}/30 次尝试]: {result.get('description')}")
        except requests.exceptions.RequestException as e:
            logging.error(f"发送 Telegram 视频时网络错误 [第 {attempt+1}/30 次尝试]: {e}")
        
        if attempt < 29:
            time.sleep(3)
    return False

def send_telegram_message(bot_token, chat_id, text):
    """使用 HTML 格式发送消息到 Telegram"""
    api_url = f"https://api.telegram.org/bot{bot_token}/sendMessage"
    payload = {
        'chat_id': chat_id,
        'text': text,
        'parse_mode': 'HTML',
        'disable_web_page_preview': False
    }
    for attempt in range(30):
        try:
            response = requests.post(api_url, data=payload, timeout=10)
            response.raise_for_status()
            result = response.json()
            if result.get('ok'):
                logging.info(f"成功发送消息到 Chat ID: {chat_id}")
                return True
            else:
                logging.error(f"Telegram API 错误 [第 {attempt+1}/30 次尝试]: {result.get('description')}")
        except requests.exceptions.RequestException as e:
            logging.error(f"发送 Telegram 消息时网络错误 [第 {attempt+1}/30 次尝试]: {e}")
        
        if attempt < 29:
            time.sleep(3)
    return False

def monitor_worker():
    """后台监控工作线程"""
    logging.info("正在启动 TruthSocial 监视器...")
    
    initial_config = config_manager.load_config()
    
    if not initial_config.get('telegram', {}).get('bot_token') or not initial_config.get('telegram', {}).get('chat_id') or initial_config.get('telegram', {}).get('bot_token') == "YOUR_TELEGRAM_BOT_TOKEN":
        logging.error("错误：Telegram 的 'bot_token' 或 'chat_id' 未在 config.yaml 中正确配置。")
        sys.exit(1)

    monitor = TruthSocialMonitor(config)
    refresh_interval_str = config.get('refresh_interval', '5m')
    interval_seconds = parse_duration(refresh_interval_str)

    try:
        while True:
            logging.info("--- 开始新的监控周期 ---")
            # 每次循环重新加载配置，以便动态更新监控列表和 Telegram 设置 (例如通过 Web UI 修改后)
            current_config = config_manager.load_config()
            bot_token = current_config.get('telegram', {}).get('bot_token')
            chat_id = current_config.get('telegram', {}).get('chat_id')

            accounts_to_monitor = current_config.get('accounts_to_monitor', []) # 现在这里是URL列表

            # 增加对 accounts_to_monitor 类型的检查，防止配置错误
            if not isinstance(accounts_to_monitor, list):
                logging.error("配置错误: 'accounts_to_monitor' 在 config.yaml 中必须是一个列表 (list)，即使只有一个URL。")
                logging.error("请检查您的 config.yaml 文件，确保格式正确，例如:\naccounts_to_monitor:\n  - https://truthsocial.com/@realDonaldTrump")
                time.sleep(interval_seconds) # 等待下一个周期，给用户时间修复配置
                continue

            if not accounts_to_monitor:
                logging.warning("监控列表为空。请在 config.yaml 中配置要监控的URL。")

            # 抓取所有监控账户的帖子，并使用 INSERT OR IGNORE 存入数据库
            for profile_url in accounts_to_monitor:
                if not isinstance(profile_url, str) or not profile_url.startswith('http'):
                    logging.error(f"配置错误: 监控列表中的条目 '{profile_url}' 不是一个有效的URL。请确保所有条目都是完整的URL，例如 'https://truthsocial.com/@username'。跳过此条目。")
                    continue
                posts = monitor.fetch_latest_posts(profile_url)
                for post in posts:
                    if post.get('id'):
                        database.add_post(post) # 新帖子会被添加，旧帖子会被忽略
            
            # 2. 从数据库获取所有未发送的帖子并进行推送
            unsent_posts = database.get_unsent_posts()
            if unsent_posts:
                # 按时间顺序推送，先抓取到的先推送
                sorted_unsent_posts = sorted(unsent_posts, key=lambda p: p.get('timestamp') or '')
                for post in sorted_unsent_posts:
                    logging.info(f"发现未推送的帖子! 用户: {post['username']}, ID: {post['id']}")
                    
                    success = False
                    # 检查帖子是否包含视频
                    if post.get('video_url'):
                        # 视频帖子
                        caption = f"<b>{post['username']} 发布了新视频:</b>\n\n{post['content']}\n\n<a href='{post['web_url']}'>查看原文</a>"
                        success = send_telegram_video(bot_token, chat_id, post['video_url'], caption)
                    else:
                        # 普通文本帖子
                        message = f"<b>{post['username']} 发布了新内容:</b>\n\n{post['content']}\n\n<a href='{post['web_url']}'>查看原文</a>"
                        success = send_telegram_message(bot_token, chat_id, message)

                    if success:
                        database.mark_post_as_sent(post['id'])

                    time.sleep(1) # 短暂延时，防止消息发送过快
            else:
                logging.info("未发现新帖子。")
                
            logging.info(f"--- 监控周期结束。休眠 {refresh_interval_str} ---")
            time.sleep(interval_seconds)
    except KeyboardInterrupt:
        logging.info("\n程序已由用户手动停止。")

if __name__ == "__main__":
    # 1. 运行一次性设置任务
    create_templates_if_not_exists()
    database.init_db()

    # 2. 在后台线程中启动监控循环
    logging.info("在后台启动监控线程...")
    monitor_thread = threading.Thread(target=monitor_worker, daemon=True)
    monitor_thread.start()

    # 3. 在主线程中启动 Flask Web UI
    logging.info("启动Web UI，请访问: http://127.0.0.1:8085")
    # 在生产环境中，建议使用 Waitress 或 Gunicorn 等 WSGI 服务器
    app.run(host='0.0.0.0', port=8085)