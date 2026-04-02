# d:\Trumptruthsocial\scraper.py
from selenium import webdriver
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import TimeoutException, NoSuchElementException
import time

# 这是一个示例函数，您需要将其集成到您的爬虫框架中
def scrape_posts_from_profile(driver, username):
    """
    从指定用户的个人资料页面抓取帖子。
    - 展开 "see more"
    - 精准提取内容、过滤头尾
    - 提取视频链接
    """
    print(f"开始抓取用户: @{username}")
    driver.get(f"https://truthsocial.com/@{username}")

    try:
        # 等待第一篇帖子加载完成
        WebDriverWait(driver, 20).until(
            EC.presence_of_element_located((By.CSS_SELECTOR, 'article[data-testid="post"]'))
        )
        time.sleep(2) # 等待页面稳定
    except TimeoutException:
        print("页面加载超时或未找到帖子。")
        return []

    posts_elements = driver.find_elements(By.CSS_SELECTOR, 'article[data-testid="post"]')
    scraped_data = []

    for post_element in posts_elements:
        post_data = {'username': username}

        # 3. 模拟鼠标展开
        try:
            # Truth Social 的展开按钮可能在 'post-text' 内部
            # 这个选择器需要根据实际情况微调
            expand_button = post_element.find_element(By.CSS_SELECTOR, 'div[data-testid="post-text"] button')
            driver.execute_script("arguments[0].click();", expand_button)
            time.sleep(0.5) # 等待DOM更新
        except NoSuchElementException:
            pass # 没有展开按钮，是短文

        # 4 & 5. 过滤头尾，精准抓取内容
        try:
            content_div = post_element.find_element(By.CSS_SELECTOR, 'div[data-testid="post-text"]')
            post_data['content'] = content_div.text
        except NoSuchElementException:
            post_data['content'] = ''

        # 6. 如果遇到视频帖子，抓取视频链接
        try:
            video_tag = post_element.find_element(By.CSS_SELECTOR, 'video')
            post_data['video_url'] = video_tag.get_attribute('src')
        except NoSuchElementException:
            post_data['video_url'] = None

        # 提取 Post ID 和原文链接
        try:
            # 通常原文链接包含 status 和 post_id
            link_tag = post_element.find_element(By.CSS_SELECTOR, 'a[href*="/status/"]')
            post_data['web_url'] = link_tag.get_attribute('href')
            post_data['id'] = post_data['web_url'].split('/')[-1]
        except NoSuchElementException:
            continue # 如果没有ID，则跳过此条不完整的帖子

        # 存储到数据库 (您需要调用 db_handler.save_post)
        # db_handler.save_post(post_data)
        scraped_data.append(post_data)
        print(f"抓取到帖子 ID: {post_data['id']}")

    return scraped_data