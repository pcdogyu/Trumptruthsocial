import yaml
import os
import logging
import threading

CONFIG_FILE = 'config.yaml'

# 用于保护 config.yaml 读写的锁
config_lock = threading.Lock()

def load_config():
    """加载 YAML 配置文件，使用锁确保线程安全。"""
    with config_lock:
        try:
            if not os.path.exists(CONFIG_FILE):
                logging.warning(f"配置文件 '{CONFIG_FILE}' 未找到。将返回空配置。")
                return {}
            with open(CONFIG_FILE, 'r', encoding='utf-8') as f:
                return yaml.safe_load(f) or {}
        except yaml.YAMLError as e:
            logging.error(f"错误：解析配置文件 '{CONFIG_FILE}' 时出错: {e}")
            return {}
        except Exception as e:
            logging.error(f"加载配置文件 '{CONFIG_FILE}' 时发生未知错误: {e}")
            return {}

def save_config(config_data):
    """保存 YAML 配置文件，使用锁确保线程安全。"""
    with config_lock:
        try:
            with open(CONFIG_FILE, 'w', encoding='utf-8') as f:
                yaml.dump(config_data, f, default_flow_style=False, allow_unicode=True)
        except Exception as e:
            logging.error(f"保存配置文件 '{CONFIG_FILE}' 时出错: {e}")