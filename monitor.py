import requests
from bs4 import BeautifulSoup
import yaml
import time
import logging
import os
from datetime import datetime, timedelta, timezone

# Selenium imports
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from webdriver_manager.chrome import ChromeDriverManager

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

        # Load selectors from config, with fallbacks
        self.selectors = self.config.get('selectors', {})
        self.post_container_selector = self.selectors.get('post_container', 'article[data-id]')
        self.post_id_attribute = self.selectors.get('post_id_attribute', 'data-id')
        self.post_content_div_selector = self.selectors.get('post_content_div', 'div.status__content')
        self.post_web_url_anchor_selector = self.selectors.get('post_web_url_anchor', 'a.status__relative-time')
        self.video_container_div_selector = self.selectors.get('video_container_div', 'div.media-gallery__item-video-container')
        self.video_tag_selector = self.selectors.get('video_tag', 'video')
        self.video_source_tag_selector = self.selectors.get('video_source_tag', 'source')
        self.show_more_button_selector = self.selectors.get('show_more_button', '.status__content__read-more-button')
        self.post_timestamp_tag_selector = self.selectors.get('post_timestamp_tag', 'time')
        self.post_timestamp_attribute = self.selectors.get('post_timestamp_attribute', 'datetime')

    def _init_selenium_driver(self):
        """初始化并返回一个 Selenium WebDriver 实例"""
        options = Options()
        options.add_argument("--headless=new")  # 使用更新的无头模式，更难被检测
        options.add_argument("--no-sandbox")
        options.add_argument("--disable-dev-shm-usage")
        options.add_argument("--disable-gpu")
        options.add_argument("--window-size=1920,1080")
        options.add_argument("--disable-blink-features=AutomationControlled") # 隐藏 WebDriver 特征
        options.add_experimental_option('excludeSwitches', ['enable-automation', 'enable-logging']) 
        options.add_experimental_option('useAutomationExtension', False)
        
        # 使用与 requests session 相同的 User-Agent 以保持一致性
        options.add_argument(f"user-agent={self.headers['User-Agent']}")

        try:
            service = Service(ChromeDriverManager().install())
            driver = webdriver.Chrome(service=service, options=options)
            
            # 进一步抹除 navigator.webdriver 痕迹
            driver.execute_cdp_cmd("Page.addScriptToEvaluateOnNewDocument", {
                "source": "Object.defineProperty(navigator, 'webdriver', {get: () => undefined})"
            })
            logging.info("Selenium WebDriver initialized.")
            return driver
        except Exception as e:
            logging.error(f"初始化 Selenium WebDriver 失败: {e}")
            logging.error("请确保 Chrome 浏览器已安装，并且网络连接正常。")
            return None

    def _parse_post_timestamp(self, post_element):
        """
        从帖子元素中解析时间戳。
        假设时间戳在 <time> 标签的 datetime 属性中，例如: <time datetime="2023-10-27T10:00:00Z">
        """
        time_element = post_element.select_one(self.post_timestamp_tag_selector)
        if time_element and self.post_timestamp_attribute in time_element.attrs:
            try:
                timestamp_str = time_element[self.post_timestamp_attribute]
                # Python 3.11+ 可以直接解析 'Z'。为了兼容性，如果存在则替换 'Z'
                if timestamp_str.endswith('Z'):
                    timestamp_str = timestamp_str[:-1] + '+00:00'
                
                # 确保 datetime 对象是时区感知的，以便进行正确比较
                dt_object = datetime.fromisoformat(timestamp_str)
                if dt_object.tzinfo is None: # 如果没有时区信息，则假定为 UTC
                    dt_object = dt_object.replace(tzinfo=timezone.utc)
                return dt_object
            except ValueError:
                logging.warning(f"无法解析时间戳: {time_element[self.post_timestamp_attribute]}. 将返回当前 UTC 时间作为备用。")
        # 如果没有找到时间元素或解析失败，返回当前 UTC 时间。
        # 这确保了没有明确时间戳的帖子被视为最新，不会过早停止滚动。
        return datetime.now(timezone.utc)

    def fetch_latest_posts(self, username):
        """
        使用 Selenium 抓取用户主页上可见的最新帖子。
        Truth Social 是动态渲染页面，且有 Cloudflare 保护，requests 容易失效。
        """
        profile_url = f"https://truthsocial.com/@{username}"
        logging.info(f"Fetching latest posts (Selenium) from: {profile_url}")
        
        driver = self._init_selenium_driver()
        if not driver:
            return []
            
        try:
            driver.get(profile_url)
            
            # 智能等待：等待帖子容器元素出现，最多等待 15 秒。有助于穿透 Cloudflare 的 "Just a moment" 加载页。
            try:
                WebDriverWait(driver, 15).until(
                    EC.presence_of_element_located((By.CSS_SELECTOR, self.post_container_selector))
                )
            except Exception:
                logging.warning(f"等待帖子加载超时。当前网页标题为: '{driver.title}'")
            
            # 稍微向下滚动，触发可能的懒加载机制
            driver.execute_script("window.scrollBy(0, 800);")
            time.sleep(2)

            # 模拟点击“展开更多”按钮，确保长文本内容完整
            try:
                click_script = f"""
                document.querySelectorAll('{self.show_more_button_selector}').forEach(function(btn) {{
                    if (btn.offsetParent !== null) btn.click();
                }});
                """
                driver.execute_script(click_script)
                time.sleep(0.5)
            except Exception as e:
                logging.debug(f"尝试点击'展开更多'按钮时发生错误: {e}")

            soup = BeautifulSoup(driver.page_source, 'html.parser')
            posts = []

            # 必须使用 select() 来解析 CSS 选择器
            post_elements = soup.select(self.post_container_selector)

            for element in post_elements:
                post_id = element.get(self.post_id_attribute)
                if not post_id:
                    continue # 如果没有 ID 则跳过

                content_element = element.select_one(self.post_content_div_selector)
                content = content_element.get_text(strip=True) if content_element else ''
                
                # 备用方案：如果未找到特定 div 的内容，直接提取整个 article 块的文本
                if not content:
                    content = element.get_text(separator='\n', strip=True)
                    if len(content) > 1000: content = content[:1000] + "..."
                
                web_url_element = element.select_one(self.post_web_url_anchor_selector)
                if web_url_element and 'href' in web_url_element.attrs:
                    web_url = web_url_element['href']
                    if web_url.startswith('/'):
                        web_url = f"https://truthsocial.com{web_url}"
                else:
                    web_url = f"https://truthsocial.com/@{username}/posts/{post_id}"

                # --- 新增：检查视频 ---
                video_url = None
                video_container = element.select_one(self.video_container_div_selector) 
                if video_container:
                    video_tag = video_container.select_one(self.video_tag_selector)
                    if video_tag:
                        source_tag = video_tag.select_one(self.video_source_tag_selector)
                        if source_tag and source_tag.get('src'):
                            video_url = source_tag['src']
                        # 否则直接从 <video> 标签获取
                        elif video_tag.get('src'):
                            video_url = video_tag['src']

                # 只要有 post_id 就添加，允许内容为空的视频帖子
                posts.append({
                    'id': post_id,
                    'content': content,
                    'username': username,
                    'web_url': web_url,
                    'video_url': video_url # 添加视频URL字段
                })
            logging.info(f"Found {len(posts)} latest posts for {username} using Selenium.")
            return posts
        except Exception as e:
            logging.error(f"Failed to fetch latest posts for {username} (Selenium): {e}")
            return []
        finally:
            driver.quit() # 确保执行完毕后关闭浏览器释放内存

    def fetch_historical_posts_selenium(self, username, days_to_sync=7, max_scrolls=50, max_posts_per_user=500):
        """
        使用 Selenium 抓取指定用户在过去 N 天内的历史帖子。
        通过模拟滚动加载更多内容。
        """
        driver = self._init_selenium_driver()
        if not driver:
            return []

        profile_url = f"https://truthsocial.com/@{username}"
        logging.info(f"Fetching historical posts (Selenium) for @{username} from: {profile_url} (last {days_to_sync} days)")
        
        driver.get(profile_url)
        time.sleep(3) # 给页面一些初始加载时间

        collected_posts = {} # 使用字典存储唯一帖子，以 post_id 为键
        stop_fetching = False
        target_datetime = datetime.now(timezone.utc) - timedelta(days=days_to_sync)

        for i in range(max_scrolls):
            if stop_fetching:
                logging.info(f"停止抓取 @{username} 的历史帖子。原因：达到目标日期。")
                break
            if len(collected_posts) >= max_posts_per_user:
                logging.info(f"停止抓取 @{username} 的历史帖子。原因：达到最大帖子数限制 ({max_posts_per_user} 条)。")
                break

            logging.debug(f"Scrolling down for @{username}, scroll attempt {i+1}/{max_scrolls}...")
            last_height = driver.execute_script("return document.body.scrollHeight")
            # 滚动到页面底部
            driver.execute_script("window.scrollTo(0, document.body.scrollHeight);")
            time.sleep(2) # 等待新内容加载
            new_height = driver.execute_script("return document.body.scrollHeight")
            if new_height == last_height:
                logging.info(f"滚动后 @{username} 的页面高度未变化，可能已到达内容末尾。")
                break

            # --- 模拟点击“展开更多”按钮 ---
            try:
                # 使用 JavaScript 点击所有可见的“展开更多”按钮
                # 这比使用 Selenium 的 click() 更稳健，不容易被遮挡拦截
                click_script = f"""
                document.querySelectorAll('{self.show_more_button_selector}').forEach(function(btn) {{
                    if (btn.offsetParent !== null) btn.click();
                }});
                """
                driver.execute_script(click_script)
                time.sleep(0.5) # 给页面一点时间展开折叠的文本
            except Exception as e:
                logging.debug(f"尝试点击'展开更多'按钮时发生错误: {e}")

            # 解析当前页面源
            soup = BeautifulSoup(driver.page_source, 'html.parser')
            post_elements = soup.select(self.post_container_selector)

            posts_in_current_scroll = 0
            for element in post_elements:
                post_id = element.get('data-id')
                if not post_id or post_id in collected_posts:
                    continue # 跳过没有 ID 的帖子或已处理过的帖子

                post_datetime = self._parse_post_timestamp(element)
                # 如果帖子时间戳早于目标日期，则停止抓取
                if post_datetime < target_datetime:
                    logging.info(f"发现 @{username} 的帖子早于 {days_to_sync} 天。停止滚动。")
                    stop_fetching = True
                    break # 停止处理当前滚动中的帖子，并跳出外部滚动循环

                content_element = element.select_one(self.post_content_div_selector)
                content = content_element.get_text(strip=True) if content_element else ''
                
                # 备用方案：如果未找到特定 div 的内容，直接提取整个 article 块的文本
                if not content:
                    content = element.get_text(separator='\n', strip=True)
                    if len(content) > 1000: content = content[:1000] + "..."
                
                web_url_element = element.select_one(self.post_web_url_anchor_selector)
                if web_url_element and 'href' in web_url_element.attrs:
                    web_url = web_url_element['href']
                    if web_url.startswith('/'):
                        web_url = f"https://truthsocial.com{web_url}"
                else:
                    web_url = f"https://truthsocial.com/@{username}/posts/{post_id}"

                video_url = None
                video_container = element.select_one(self.video_container_div_selector) 
                if video_container:
                    video_tag = video_container.select_one(self.video_tag_selector)
                    if video_tag:
                        source_tag = video_tag.select_one(self.video_source_tag_selector)
                        if source_tag and source_tag.get('src'):
                            video_url = source_tag['src']
                        elif video_tag.get('src'):
                            video_url = video_tag['src']

                collected_posts[post_id] = {
                    'id': post_id,
                    'content': content,
                    'username': username,
                    'web_url': web_url,
                    'video_url': video_url,
                    'timestamp': post_datetime.isoformat() # 存储 ISO 格式的时间戳
                }
                posts_in_current_scroll += 1
            
            if posts_in_current_scroll == 0 and i > 0: # 如果滚动后没有发现新帖子，可能已到达内容末尾
                logging.info(f"滚动 {i+1} 次后未发现新帖子。假定已到达 @{username} 的内容末尾。")
                break

        driver.quit()
        logging.info(f"为 @{username} 收集到 {len(collected_posts)} 条历史帖子（在 {days_to_sync} 天内或达到最大滚动/帖子限制）。")
        return list(collected_posts.values())

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

    monitor = TruthSocialMonitor(config) # 实例化监控器

    refresh_interval = config.get('refresh_interval', '5m')
    
    def parse_duration_local(interval_str):
        """将 '5m', '1h' 这样的字符串解析为秒数 (monitor.py 内部使用)"""
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
    interval_seconds = parse_duration_local(refresh_interval)

    while True:
        monitor.run_cycle()
        logging.info(f"Waiting for {refresh_interval} before next cycle...")
        time.sleep(interval_seconds)