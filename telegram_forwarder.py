# d:\Trumptruthsocial\telegram_forwarder.py
import requests
import html
import logging
import config_manager # 导入新的配置管理器

def _load_telegram_config():
    """从 config.yaml 加载 Telegram 配置"""
    # 使用 config_manager 加载完整配置，然后提取 Telegram 部分
    full_config = config_manager.load_config()
    if not full_config:
        return {} # 如果加载失败，返回空字典
    return full_config.get('telegram', {})

def forward_post(post):
    """
    将帖子内容格式化并发送到Telegram。
    'post' 是一个从数据库获取的类字典对象 (sqlite3.Row)。
    """
    text_to_send = f"<b>来自: @{post['username']}</b>\n\n"

    if post.get('content'):
        # 对内容进行HTML转义，防止内容中的特殊字符破坏格式
        text_to_send += html.escape(post['content']) + "\n\n"

    if post.get('video_url'):
        text_to_send += f"视频链接: {post['video_url']}\n\n"
    
    # 附上原文链接
    text_to_send += f"<a href='{post['web_url']}'>查看原文</a>"

    telegram_config = _load_telegram_config()
    bot_token = telegram_config.get('bot_token')
    chat_id = telegram_config.get('chat_id')

    if not bot_token or not chat_id or 'YOUR_TELEGRAM_BOT_TOKEN' in bot_token:
        error_msg = "Telegram 未在 config.yaml 中正确配置。"
        logging.error(error_msg)
        return False, error_msg

    api_url = f"https://api.telegram.org/bot{bot_token}/sendMessage"
    
    payload = {
        'chat_id': chat_id,
        'text': text_to_send,
        'parse_mode': 'HTML',
        'disable_web_page_preview': False # 允许链接预览
    }

    try:
        response = requests.post(api_url, json=payload, timeout=10)
        response.raise_for_status()  # 如果请求失败 (4xx or 5xx), 抛出异常
        
        if response.json().get('ok'):
            return True, "帖子已成功转发到 Telegram。"
        else:
            return False, f"Telegram API 错误: {response.text}"
    except requests.exceptions.RequestException as e:
        return False, f"发送到 Telegram 失败: {e}"