#!/usr/bin/env python3
"""Selenium smoke for the web email signup path.

This intentionally mirrors the core Playwright signup test with Selenium
Manager so it does not require a checked-in chromedriver. It is useful for
validating WebDriver compatibility and for CI systems that standardize on
Selenium, while Playwright remains the broader browser E2E suite.
"""

import json
import os
import sys
import time
import uuid
from urllib import request

from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait


BASE_URL = os.environ.get("E2E_BASE_URL", "http://127.0.0.1:3000").rstrip("/")
CONVEX_URL = os.environ.get(
    "E2E_CONVEX_URL",
    os.environ.get(
        "NEXT_PUBLIC_CONVEX_SITE_URL",
        "https://perceptive-minnow-557.eu-west-1.convex.site",
    ),
).rstrip("/")


def delete_account(token: str) -> None:
    if not token:
        return
    req = request.Request(
        f"{CONVEX_URL}/auth/delete-account",
        method="POST",
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        data=b"{}",
    )
    try:
        request.urlopen(req, timeout=10).read()
    except Exception as exc:
        print(f"[selenium] cleanup failed: {exc}", file=sys.stderr)


def main() -> int:
    uid = uuid.uuid4()
    email = f"e2e-selenium-{uid}@yaver.test"
    password = f"pw-{uuid.uuid4()}A1"
    full_name = f"Selenium Signup {str(uid)[:8]}"
    token = ""

    opts = Options()
    opts.add_argument("--headless=new")
    opts.add_argument("--no-sandbox")
    opts.add_argument("--disable-dev-shm-usage")
    opts.add_argument("--window-size=1440,1100")
    opts.set_capability("goog:loggingPrefs", {"browser": "ALL"})

    driver = webdriver.Chrome(options=opts)
    wait = WebDriverWait(driver, 25)
    try:
        driver.get(f"{BASE_URL}/auth")
        wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space()='Sign Up']"))).click()
        wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, "input[placeholder='Full name']"))).send_keys(full_name)
        driver.find_element(By.CSS_SELECTOR, "input[placeholder='Email address']").send_keys(email)
        driver.find_element(By.CSS_SELECTOR, "input[placeholder='Password']").send_keys(password)
        driver.find_element(By.CSS_SELECTOR, "input[placeholder='Confirm password']").send_keys(password)
        driver.find_elements(By.XPATH, "//button[normalize-space()='Sign Up']")[-1].click()

        end = time.time() + 25
        while time.time() < end:
            if "/survey" in driver.current_url or "/dashboard" in driver.current_url:
                break
            time.sleep(0.25)
        else:
            driver.save_screenshot("/tmp/yaver-selenium-signup-failure.png")
            print(f"[selenium] signup did not redirect, url={driver.current_url}")
            print(driver.find_element(By.TAG_NAME, "body").text[:1500])
            return 1

        token = driver.execute_script("return window.localStorage.getItem('yaver_auth_token')")
        if not token:
            print("[selenium] no yaver_auth_token in localStorage")
            return 1

        req = request.Request(
            f"{CONVEX_URL}/auth/validate",
            headers={"Authorization": f"Bearer {token}"},
        )
        body = json.loads(request.urlopen(req, timeout=10).read().decode("utf-8"))
        got = body.get("user", {}).get("email")
        if got != email:
            print(f"[selenium] validate email mismatch: got={got} want={email}")
            return 1

        print(f"[selenium] PASS signup minted valid token for {email}")
        return 0
    finally:
        driver.quit()
        delete_account(token)


if __name__ == "__main__":
    raise SystemExit(main())
