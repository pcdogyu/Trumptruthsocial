import yaml
import requests
import json

CONFIG_FILE = 'config.yaml'

def load_config(file_path):
    """加载 YAML 配置文件"""
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            return yaml.safe_load(f)
    except FileNotFoundError:
        print(f"错误: 配置文件 '{file_path}' 未找到。")
        return None
    except Exception as e:
        print(f"错误: 解析配置文件时出错: {e}")
        return None

def check_chat_info(bot_token, chat_id):
    """使用 Telegram Bot API 的 getChat 方法获取群组信息"""
    api_url = f"https://api.telegram.org/bot{bot_token}/getChat"
    params = {'chat_id': chat_id}
    
    print(f"正在查询 Chat ID: {chat_id}...")
    
    try:
        response = requests.get(api_url, params=params, timeout=10)
        response.raise_for_status()  # 如果请求失败 (状态码非 2xx)，则抛出异常
        
        data = response.json()
        
        if data.get("ok"):
            print("
--- 查询成功 ---")
            # 使用 json.dumps 美化输出
            print(json.dumps(data['result'], indent=2, ensure_ascii=False))
            print("
请检查上面的 'title' 和 'type' (group, supergroup, channel) 是否符合您的预期。")
        else:
            print("
--- 查询失败 ---")
            print(f"API 返回错误: {data.get('description')}")
            print("
可能的原因：")
            print("1. Bot Token (机器人令牌) 无效或已过期。")
            print("2. Chat ID (群组ID) 不正确。")
            print("3. 机器人没有被添加到对应的群组或频道中。")
            print("4. 如果是私有频道，机器人需要有管理员权限。")

    except requests.exceptions.RequestException as e:
        print(f"
网络请求错误: {e}")
        print("请检查您的网络连接和代理设置。")
    except Exception as e:
        print(f"
发生未知错误: {e}")

if __name__ == "__main__":
    config = load_config(CONFIG_FILE)
    if config and 'telegram' in config:
        bot_token = config['telegram'].get('bot_token')
        chat_id = config['telegram'].get('chat_id')
        
        if not bot_token or not chat_id:
            print("错误: 配置文件中缺少 'bot_token' 或 'chat_id'。")
        else:
            check_chat_info(bot_token, chat_id)
    else:
        print("错误: 无法加载或解析 'telegram' 配置部分。")
