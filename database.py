import sqlite3
import logging
import os
from datetime import datetime

DATABASE_FILE = 'monitor.db'

def get_db_connection():
    """创建并返回一个数据库连接"""
    conn = sqlite3.connect(DATABASE_FILE)
    conn.row_factory = sqlite3.Row # 允许通过列名访问数据
    return conn

def init_db():
    """初始化数据库，创建表（如果不存在）"""
    if os.path.exists(DATABASE_FILE):
        return
        
    logging.info("正在创建新的 SQLite 数据库...")
    conn = get_db_connection()
    try:
        with conn:
            conn.execute('''
                CREATE TABLE posts (
                    id TEXT PRIMARY KEY,
                    username TEXT NOT NULL,
                    content TEXT,
                    web_url TEXT,
                    video_url TEXT,
                    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
                )
            ''')
        logging.info("数据库表 'posts' 创建成功。")
    except sqlite3.Error as e:
        logging.error(f"数据库初始化失败: {e}")
    finally:
        conn.close()

def add_post(post):
    """
    将一个新帖子添加到数据库。
    如果帖子已存在，则不执行任何操作。
    """
    conn = get_db_connection()
    try:
        with conn:
            conn.execute(
                "INSERT INTO posts (id, username, content, web_url, video_url) VALUES (?, ?, ?, ?, ?)",
                (post['id'], post['username'], post.get('content'), post.get('web_url'), post.get('video_url'))
            )
    except sqlite3.IntegrityError:
        # 主键冲突，意味着帖子已存在，这在我们的逻辑中是正常的，无需记录错误
        pass
    except sqlite3.Error as e:
        logging.error(f"添加帖子到数据库时出错: {e}")
    finally:
        conn.close()

def is_post_seen(post_id):
    """检查指定的 post_id 是否已存在于数据库中"""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT 1 FROM posts WHERE id = ?", (post_id,))
    result = cursor.fetchone() is not None
    conn.close()
    return result

def get_recent_posts(limit=100):
    """从数据库获取最近的帖子记录"""
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT * FROM posts ORDER BY created_at DESC LIMIT ?", (limit,))
    # 将 sqlite3.Row 对象转换为字典列表
    posts = [dict(row) for row in cursor.fetchall()]
    conn.close()
    return posts