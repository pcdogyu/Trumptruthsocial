import argparse
import json
import logging
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import config_manager
from selenium import webdriver
from selenium.common.exceptions import WebDriverException
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.chrome.service import Service
from webdriver_manager.chrome import ChromeDriverManager

DEFAULT_LOGIN_URL = "https://truthsocial.com/login"
DEFAULT_TIMEOUT_SECONDS = 180
DEFAULT_POLL_INTERVAL_SECONDS = 1
DEFAULT_PROFILE_DIR = Path(".chrome-token-profile")

logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s - %(message)s")


def build_driver(profile_dir: Path) -> webdriver.Chrome:
    service = Service(ChromeDriverManager().install())
    options = Options()
    options.add_experimental_option("excludeSwitches", ["enable-logging"])
    options.add_argument(f"--user-data-dir={profile_dir.resolve()}")
    return webdriver.Chrome(service=service, options=options)


def extract_access_token(driver) -> Optional[str]:
    """从 Truth Social 登录后的 localStorage 中提取 access_token。"""
    try:
        auth_data_str = driver.execute_script("return localStorage.getItem('truth:auth');")
        if not auth_data_str:
            return None

        auth_data = json.loads(auth_data_str)
        users = auth_data.get("users") if isinstance(auth_data, dict) else None
        if not isinstance(users, dict):
            return None

        for user_info in users.values():
            if not isinstance(user_info, dict):
                continue
            access_token = user_info.get("access_token")
            if access_token:
                return access_token
    except Exception:
        return None
    return None


def mask_token(token: str) -> str:
    token = token.strip()
    if len(token) <= 12:
        return token
    return f"{token[:6]}...{token[-4:]}"


def save_token_to_config(token: str) -> None:
    config = config_manager.load_config()
    auth = config.setdefault("auth", {})
    existing = [
        token.strip(),
        str(auth.get("bearer_token", "")).strip(),
        str(auth.get("bearer_token_backup_1", "")).strip(),
        str(auth.get("bearer_token_backup_2", "")).strip(),
    ]
    rotated = []
    seen = set()
    for item in existing:
        if not item or "YOUR_TRUTHSOCIAL_BEARER_TOKEN" in item:
            continue
        if item in seen:
            continue
        seen.add(item)
        rotated.append(item)
        if len(rotated) == 3:
            break

    auth["bearer_token"] = rotated[0] if len(rotated) > 0 else ""
    auth["bearer_token_backup_1"] = rotated[1] if len(rotated) > 1 else ""
    auth["bearer_token_backup_2"] = rotated[2] if len(rotated) > 2 else ""
    auth["bearer_token_updated_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    config_manager.save_config(config)
    logging.info("Bearer Token 已写回 config.yaml")


def wait_for_token(driver, timeout_seconds: int, poll_interval_seconds: int) -> Optional[str]:
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        access_token = extract_access_token(driver)
        if access_token:
            return access_token
        time.sleep(poll_interval_seconds)
    return None


def run() -> int:
    parser = argparse.ArgumentParser(
        description="打开 Truth Social 登录页，等待登录后自动抓取 Bearer Token 并写回 config.yaml。"
    )
    parser.add_argument("--login-url", default=DEFAULT_LOGIN_URL, help="Truth Social 登录地址")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="最长等待登录的秒数")
    parser.add_argument(
        "--poll-interval",
        type=int,
        default=DEFAULT_POLL_INTERVAL_SECONDS,
        help="轮询 localStorage 的间隔秒数",
    )
    parser.add_argument(
        "--profile-dir",
        default=str(DEFAULT_PROFILE_DIR),
        help="Chrome 用户数据目录。用于保留登录态，便于下次自动抓取。",
    )
    parser.add_argument(
        "--print-token",
        action="store_true",
        help="同时在控制台打印完整 Token。默认只输出脱敏结果。",
    )
    args = parser.parse_args()

    profile_dir = Path(args.profile_dir)
    profile_dir.mkdir(parents=True, exist_ok=True)

    driver = None
    try:
        driver = build_driver(profile_dir)
        driver.get(args.login_url)

        logging.info("=" * 60)
        logging.info("浏览器窗口已打开。请在该窗口中登录 Truth Social。")
        logging.info("登录后脚本会自动检测 localStorage 并写回 Bearer Token。")
        logging.info("如果你已经登录过，脚本可能会在几秒内直接完成。")
        logging.info("=" * 60)

        access_token = wait_for_token(driver, args.timeout, args.poll_interval)
        if not access_token:
            logging.error("在 %s 秒内未检测到 Token。请确认已经完成登录。", args.timeout)
            return 1

        save_token_to_config(access_token)
        logging.info("已获取 Bearer Token: %s", mask_token(access_token))
        if args.print_token:
            print(f"\nBearer Token: {access_token}\n")
        return 0
    except WebDriverException as exc:
        logging.error("启动浏览器失败: %s", exc)
        return 1
    finally:
        if driver:
            driver.quit()
            logging.info("浏览器已关闭。")


if __name__ == "__main__":
    sys.exit(run())
