import requests
from bs4 import BeautifulSoup
import yaml
import time
import logging
import os

# 配置日志
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

CONFIG_FILE = 'config.yaml'
STATE_FILE = 'state.json' # 暂时不实现状态保存，但保留字段

class TruthSocialMonitor:
    def __init__(self, config):
        self.config = config
        self.session = requests.Session()
        self.headers = {
            'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/100.0.4896.127 Safari/537.36',
            'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9',
            'Accept-Language': 'en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7',
            'Connection': 'keep-alive',
        }
        # 检查并添加认证 Token
        bearer_token = self.config.get('auth', {}).get('bearer_token')
        if bearer_token and 'YOUR_TRUTHSOCIAL_BEARER_TOKEN' not in bearer_token:
            self.headers['Authorization'] = f'Bearer {bearer_token}'
            logging.info("已找到 Bearer Token，将用于认证请求。")

        self.session.headers.update(self.headers)
        logging.info("Monitor initialized with config.")

    def login(self):
        # Truth Social 的登录通常涉及复杂的 JavaScript 和 CSRF 令牌。
        # 简单的 POST 请求可能不足以完成登录。
        # 这里的实现仅为占位符，您需要根据实际抓包分析来完善。
        login_url = "https://truthsocial.com/api/v1/sessions" # 猜测的登录API
        payload = {
            "username": self.config['auth']['username'],
            "password": self.config['auth']['password']
        }
        try:
            response = self.session.post(login_url, json=payload, timeout=10)
            response.raise_for_status() # 如果状态码不是2xx，则抛出HTTPError
            logging.info(f"Login attempt successful (status: {response.status_code}). Session cookies obtained.")
            # 实际登录成功后，session对象会自动保存cookie
            return True
        except requests.exceptions.RequestException as e:
            logging.error(f"Login failed: {e}")
            logging.error(f"Response content: {response.text if 'response' in locals() else 'No response'}")
            return False

    def fetch_latest_posts(self, username):
        profile_url = f"https://truthsocial.com/@{username}"
        logging.info(f"Fetching posts from: {profile_url}")
        try:
            response = self.session.get(profile_url, timeout=15)
            response.raise_for_status()
            
            soup = BeautifulSoup(response.text, 'html.parser')
            posts = []

            # !!! 关键: 以下 CSS 选择器是基于 Go 代码的猜测，您需要根据 Truth Social 网页的实际 HTML 结构进行调整 !!!
            # 使用浏览器开发者工具检查帖子元素的 class 或 id
            # 这个选择器应该指向包含单个帖子的整个 <article> 或 <div> 元素
            post_elements = soup.find_all('article', attrs={'data-id': True}) # 示例选择器，寻找带有 data-id 属性的 article 标签

            for element in post_elements:
                post_id = element.get('data-id') # 假设帖子ID在 data-id 属性中
                
                content_element = element.find('div', class_='status__content') # 假设内容在特定 class 的 div 中
                content = content_element.get_text(strip=True) if content_element else ''
                
                web_url_element = element.find('a', class_='status__relative-time') # 假设链接在特定 class 的 <a> 标签中
                web_url = web_url_element['href'] if web_url_element and 'href' in web_url_element.attrs else profile_url

                # --- 新增：检查视频 ---
                video_url = None
                # 示例：寻找视频容器。您需要根据实际网页结构调整 class 名称
                video_container = element.find('div', class_='media-gallery__item-video-container') 
                if video_container:
                    video_tag = video_container.find('video')
                    if video_tag:
                        # 优先从 <source> 标签获取 URL
                        source_tag = video_tag.find('source')
                        if source_tag and source_tag.get('src'):
                            video_url = source_tag['src']
                        # 否则直接从 <video> 标签获取
                        elif video_tag.get('src'):
                            video_url = video_tag['src']

                # 只要有 post_id 就添加，允许内容为空的视频帖子
                if post_id:
                    posts.append({
                        'id': post_id,
                        'content': content,
                        'username': username,
                        'web_url': web_url,
                        'video_url': video_url # 添加视频URL字段
                    })
            logging.info(f"Found {len(posts)} posts for {username}.")
            return posts
        except requests.exceptions.RequestException as e:
            logging.error(f"Failed to fetch posts for {username}: {e}")
            return []

    def run_cycle(self):
        logging.info("--- Starting new monitoring cycle ---")
        for username in self.config['accounts_to_monitor']:
            posts = self.fetch_latest_posts(username)
            if posts:
                logging.info(f"Processing {len(posts)} posts for {username}...")
                for post in posts:
                    logging.info(f"  Post ID: {post['id']}, Content: {post['content'][:50]}...")
                    # 这里可以添加翻译、AI分析、发送Telegram消息等逻辑
            else:
                logging.info(f"No posts to process for {username}.")
        logging.info("--- Monitoring cycle finished ---")

def load_config(file_path):
    if not os.path.exists(file_path):
        logging.error(f"Config file '{file_path}' not found. Please create it.")
        return None
    with open(file_path, 'r', encoding='utf-8') as f:
        return yaml.safe_load(f)

if __name__ == "__main__":
    config = load_config(CONFIG_FILE)
    if not config:
        exit(1)

    monitor = TruthSocialMonitor(config)
    
    # 尝试登录，如果需要的话
    # if not monitor.login():
    #     logging.error("Failed to login, exiting.")
    #     exit(1)

    refresh_interval = config.get('refresh_interval', '5m')
    interval_seconds = int(time.ParseDuration(refresh_interval).total_seconds()) # 假设 refresh_interval 是 Go 风格的持续时间字符串

    while True:
        monitor.run_cycle()
        logging.info(f"Waiting for {refresh_interval} before next cycle...")
        time.sleep(interval_seconds)