# d:\Trumptruthsocial\telegram_forwarder.py
import requests
import html
import config

def forward_post(post):
    """
    将帖子内容格式化并发送到Telegram。
    'post' 是一个从数据库获取的类字典对象 (sqlite3.Row)。
    """
    # 优先转发视频链接，同时附上可能存在的文本内容
    text_to_send = f"<b>来自: @{post['username']}</b>\n\n"

    if post.get('content'):
        # 对内容进行HTML转义，防止内容中的特殊字符破坏格式
        text_to_send += html.escape(post['content']) + "\n\n"

    if post.get('video_url'):
        text_to_send += f"视频链接: {post['video_url']}\n\n"
    
    # 附上原文链接
    text_to_send += f"<a href='{post['web_url']}'>查看原文</a>"

    api_url = f"https://api.telegram.org/bot{config.TELEGRAM_BOT_TOKEN}/sendMessage"
    
    payload = {
        'chat_id': config.TELEGRAM_CHAT_ID,
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