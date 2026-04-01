import sqlite3
import logging
import json
from datetime import datetime, timedelta

DATABASE_NAME = 'monitor.db'

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

def init_db():
    """初始化数据库，创建 posts 表"""
    conn = sqlite3.connect(DATABASE_NAME)
    cursor = conn.cursor()
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS posts (
            id TEXT PRIMARY KEY,
            username TEXT NOT NULL,
            content TEXT,
            web_url TEXT,
            video_url TEXT,
            timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
            raw_data TEXT
        )
    ''')
    conn.commit()
    conn.close()
    logging.info("数据库初始化完成。")

def add_post(post_data):
    """向数据库添加一个帖子"""
    conn = sqlite3.connect(DATABASE_NAME)
    cursor = conn.cursor()
    try:
        cursor.execute('''
            INSERT OR IGNORE INTO posts (id, username, content, web_url, video_url, raw_data)
            VALUES (?, ?, ?, ?, ?, ?)
        ''', (
            post_data.get('id'),
            post_data.get('username'),
            post_data.get('content'),
            post_data.get('web_url'),
            post_data.get('video_url'),
            json.dumps(post_data) # 存储原始数据以备将来使用
        ))
        conn.commit()
        if cursor.rowcount > 0:
            logging.info(f"帖子 {post_data.get('id')} 已添加到数据库。")
            return True
        else:
            logging.debug(f"帖子 {post_data.get('id')} 已存在，跳过。")
            return False
    except sqlite3.Error as e:
        logging.error(f"添加帖子到数据库时出错: {e}")
        return False
    finally:
        conn.close()

def is_post_seen(post_id):
    """检查帖子是否已存在于数据库中"""
    conn = sqlite3.connect(DATABASE_NAME)
    cursor = conn.cursor()
    cursor.execute('SELECT 1 FROM posts WHERE id = ?', (post_id,))
    result = cursor.fetchone()
    conn.close()
    return result is not None

def get_all_posts(username=None, limit=100, offset=0):
    """从数据库获取所有帖子，可按用户名过滤"""
    conn = sqlite3.connect(DATABASE_NAME)
    cursor = conn.cursor()
    query = 'SELECT id, username, content, web_url, video_url, timestamp FROM posts'
    params = []
    if username:
        query += ' WHERE username = ?'
        params.append(username)
    query += ' ORDER BY timestamp DESC LIMIT ? OFFSET ?'
    params.extend([limit, offset])
    
    cursor.execute(query, params)
    posts = []
    for row in cursor.fetchall():
        posts.append({
            'id': row[0],
            'username': row[1],
            'content': row[2],
            'web_url': row[3],
            'video_url': row[4],
            'timestamp': row[5]
        })
    conn.close()
    return posts

def get_unique_usernames():
    """获取数据库中所有唯一的用户名"""
    conn = sqlite3.connect(DATABASE_NAME)
    cursor = conn.cursor()
    cursor.execute('SELECT DISTINCT username FROM posts ORDER BY username ASC')
    usernames = [row[0] for row in cursor.fetchall()]
    conn.close()
    return usernames