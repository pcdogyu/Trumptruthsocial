import yaml
import time
import os
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from webdriver_manager.chrome import ChromeDriverManager

def load_config():
    """加载 YAML 配置文件"""
    with open('config.yaml', 'r', encoding='utf-8') as f:
        return yaml.safe_load(f)

def login_with_2fa(config):
    """
    演示使用 2FA (短信验证) 登录网站。
    这是一个概念性示例。Truth Social 网站的实际元素选择器 (ID, name, XPath等)
    必须通过在浏览器中“检查元素”来找到。
    """
    # 从环境变量中安全地读取凭据
    username = os.environ.get('TRUTHSOCIAL_USERNAME')
    password = os.environ.get('TRUTHSOCIAL_PASSWORD')

    if not username or not password:
        print("错误: 请设置 TRUTHSOCIAL_USERNAME 和 TRUTHSOCIAL_PASSWORD 环境变量。")
        return

    # 自动设置 Chrome 驱动
    print("正在设置浏览器驱动...")
    service = Service(ChromeDriverManager().install())
    driver = webdriver.Chrome(service=service)
    
    # 请将此URL替换为Truth Social的实际登录网址
    login_url = "https://truthsocial.com/login" 
    driver.get(login_url)

    try:
        # --- 步骤 1: 输入用户名和密码 ---
        print("正在输入用户名和密码...")
        
        # 等待用户名字段可见并输入 (注意: 'username' 是一个示例选择器)
        user_field = WebDriverWait(driver, 10).until(
            EC.presence_of_element_located((By.NAME, "username"))
        )
        user_field.send_keys(username)

        # 找到密码字段并输入 (注意: 'password' 是一个示例选择器)
        pass_field = driver.find_element(By.NAME, "password")
        pass_field.send_keys(password)

        # 找到并点击登录按钮 (注意: 这是一个示例XPath)
        login_button = driver.find_element(By.XPATH, "//button[@type='submit']")
        login_button.click()
        
        # --- 步骤 2: 处理 2FA ---
        print("等待二次验证 (2FA/MFA) 页面...")
        
        # 等待 2FA 验证码输入框出现，这确认我们已进入2FA页面
        # 注意: 'mfa_code' 是一个示例ID，你需要替换为真实的ID
        mfa_input_field = WebDriverWait(driver, 10).until(
            EC.presence_of_element_located((By.ID, "mfa_code"))
        )
        
        print("检测到2FA页面。请检查您的短信验证码。")
        
        # 提示用户在终端输入验证码
        mfa_code = input("请输入6位验证码并按回车: ")
        
        mfa_input_field.send_keys(mfa_code.strip())
        
        # 找到并点击“验证”按钮 (注意: 这是一个示例XPath)
        verify_button = driver.find_element(By.XPATH, "//button[text()='Verify']")
        verify_button.click()

        print("登录成功!")
        time.sleep(10) # 保持浏览器打开10秒钟以便观察

    except Exception as e:
        print(f"发生错误: {e}")
        driver.save_screenshot("login_error.png")
        print("已保存截图 'login_error.png' 用于调试。")
    finally:
        print("关闭浏览器。")
        driver.quit()

if __name__ == '__main__':
    config_data = load_config()
    login_with_2fa(config_data)