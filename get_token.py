import time
import json
import logging
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.chrome.options import Options
from webdriver_manager.chrome import ChromeDriverManager
from selenium.common.exceptions import WebDriverException

# 配置日志
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

def get_auth_token():
    """
    启动一个浏览器实例，让用户手动登录，然后从 localStorage 提取 Bearer Token。
    """
    service = Service(ChromeDriverManager().install())
    options = Options()
    # 忽略不相关的日志消息
    options.add_experimental_option('excludeSwitches', ['enable-logging'])
    
    driver = None
    try:
        driver = webdriver.Chrome(service=service, options=options)
        
        # 打开登录页面
        driver.get("https://truthsocial.com/login")
        
        logging.info("="*60)
        logging.info("浏览器窗口已打开。请在该窗口中手动完成登录操作。")
        logging.info("登录成功后，脚本将自动检测并提取 Token。")
        logging.info("="*60)

        access_token = None
        # 循环检查 localStorage，直到找到 token 为止
        for i in range(120): # 最多等待 2 分钟
            try:
                # Truth Social 将认证状态存储在名为 'truth:auth' 的 localStorage 项中
                auth_data_str = driver.execute_script("return localStorage.getItem('truth:auth');")
                if auth_data_str:
                    auth_data = json.loads(auth_data_str)
                    users = auth_data.get('users')
                    # 确保 'users' 是一个非空字典
                    if users and isinstance(users, dict) and len(users) > 0:
                        # 获取字典中的第一个用户对象 (通常只有一个)
                        first_user_key = next(iter(users))
                        user_info = users.get(first_user_key)
                        if user_info and isinstance(user_info, dict):
                            access_token = user_info.get('access_token')
                            if access_token:
                                logging.info("成功从 localStorage 中提取到 Token！")
                                break
            except Exception as e:
                # 忽略解析错误，因为在登录过程中 localStorage 的状态可能会暂时不一致
                pass
            time.sleep(1)

        if access_token:
            print("\n" + "="*20 + "  Authorization Token  " + "="*20)
            print(f"\nBearer Token: {access_token}\n")
            print("="*65)
            print("\n请将此 Token 复制到您的 config.yaml 文件或 Web UI 的 'Bearer Token' 字段中。")
            # 成功获取后等待用户确认，避免窗口立刻关闭
            input("Token 已打印在上方。按 Enter 键关闭浏览器...")
        else:
            logging.error("在超时时间内未能获取到 Token。请确保您已成功登录。")
            logging.info("将打印浏览器存储内容以供调试...")
            try:
                # 打印 localStorage
                local_storage = driver.execute_script("return window.localStorage;")
                print("\n--- localStorage 内容 ---")
                if local_storage:
                    for key, value in local_storage.items():
                        print(f"  Key: {key}")
                        # 尝试美化打印JSON
                        try:
                            parsed_value = json.loads(value)
                            print(f"  Value (JSON):\n{json.dumps(parsed_value, indent=2, ensure_ascii=False)}")
                        except (json.JSONDecodeError, TypeError):
                            print(f"  Value (Raw): {str(value)[:500]}...") # 截断过长的值
                        print("-" * 20)
                else:
                    print("localStorage 为空。")

                print("\n请检查以上输出，寻找类似 'token', 'auth', 'session' 的键，并将其内容分享出来以便分析。")
                input("调试信息已打印。按 Enter 键关闭浏览器...")

            except Exception as debug_e:
                logging.error(f"尝试打印存储内容时出错: {debug_e}")

    finally:
        if driver:
            driver.quit()
            logging.info("浏览器已关闭。")

if __name__ == "__main__":
    get_auth_token()