# d:\Trumptruthsocial\db_handler.py
import sqlite3
import config

def get_db_connection():
    """创建并返回一个数据库连接，行数据可以按列名访问。"""
    conn = sqlite3.connect(config.DATABASE_FILE)
    conn.row_factory = sqlite3.Row
    return conn

def delete_post_by_id(post_id):
    """根据帖子ID从数据库中删除一个帖子。"""
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        cursor.execute("DELETE FROM posts WHERE id = ?", (post_id,))
        conn.commit()
        deleted_rows = cursor.rowcount
        conn.close()
        return deleted_rows > 0  # 如果有行被删除，返回True
    except sqlite3.Error as e:
        print(f"数据库删除错误: {e}")
        return False

def get_post_by_id(post_id):
    """根据帖子ID从数据库中检索一个帖子。"""
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        cursor.execute("SELECT * FROM posts WHERE id = ?", (post_id,))
        post = cursor.fetchone()
        conn.close()
        return post  # 返回一个 sqlite3.Row 对象或 None
    except sqlite3.Error as e:
        print(f"数据库查询错误: {e}")
        return None

# 您还需要一个创建表的函数 (如果尚不存在)
# def init_db(): ...
# 以及一个保存帖子的函数
# def save_post(post_data): ...