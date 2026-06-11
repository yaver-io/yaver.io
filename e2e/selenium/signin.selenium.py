#!/usr/bin/env python3
"""Selenium smoke for the web email sign-in path.

Companion to ``signup-onboarding.selenium.py``: that one creates an account
through the UI; this one creates a throwaway account via the API, then drives
the *sign-in* form through Selenium and asserts the session is minted. Together
they cover both halves of the email auth flow under WebDriver, while Playwright
remains the broader browser E2E suite.

Run (server must already be up on an allowlisted host/port):

    E2E_BASE_URL=http://127.0.0.1:3217 \\
    E2E_CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site \\
      python3 e2e/selenium/signin.selenium.py
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


BASE_URL = os.environ.get("E2E_BASE_URL", "http://127.0.0.1:3217").rstrip("/")
CONVEX_URL = os.environ.get(
    "E2E_CONVEX_URL",
    os.environ.get(
        "NEXT_PUBLIC_CONVEX_SITE_URL",
        "https://perceptive-minnow-557.eu-west-1.convex.site",
    ),
).rstrip("/")


def api_signup(email: str, password: str, full_name: str) -> str:
    """Create an account via the backend and return its session token."""
    payload = json.dumps(
        {"email": email, "password": password, "fullName": full_name}
    ).encode("utf-8")
    req = request.Request(
        f"{CONVEX_URL}/auth/signup",
        method="POST",
        headers={"Content-Type": "application/json"},
        data=payload,
    )
    body = json.loads(request.urlopen(req, timeout=15).read().decode("utf-8"))
    return body["token"]


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
    email = f"e2e-selenium-signin-{uid}@yaver.test"
    password = f"pw-{uuid.uuid4()}A1"
    full_name = f"Selenium Signin {str(uid)[:8]}"

    try:
        token = api_signup(email, password, full_name)
    except Exception as exc:
        print(f"[selenium] could not provision account: {exc}", file=sys.stderr)
        return 1

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
        wait.until(
            EC.visibility_of_element_located(
                (By.CSS_SELECTOR, "input[placeholder='Email address']")
            )
        ).send_keys(email)
        driver.find_element(By.CSS_SELECTOR, "input[placeholder='Password']").send_keys(password)
        driver.find_elements(By.XPATH, "//button[normalize-space()='Sign In']")[-1].click()

        end = time.time() + 25
        while time.time() < end:
            if "/survey" in driver.current_url or "/dashboard" in driver.current_url:
                break
            time.sleep(0.25)
        else:
            driver.save_screenshot("/tmp/yaver-selenium-signin-failure.png")
            print(f"[selenium] signin did not redirect, url={driver.current_url}")
            print(driver.find_element(By.TAG_NAME, "body").text[:1500])
            return 1

        session = driver.execute_script(
            "return window.localStorage.getItem('yaver_auth_token')"
        )
        if not session:
            print("[selenium] no yaver_auth_token in localStorage after sign-in")
            return 1

        req = request.Request(
            f"{CONVEX_URL}/auth/validate",
            headers={"Authorization": f"Bearer {session}"},
        )
        body = json.loads(request.urlopen(req, timeout=10).read().decode("utf-8"))
        got = body.get("user", {}).get("email")
        if got != email:
            print(f"[selenium] validate email mismatch: got={got} want={email}")
            return 1

        print(f"[selenium] PASS sign-in minted a valid session for {email}")
        return 0
    finally:
        driver.quit()
        delete_account(token)


if __name__ == "__main__":
    raise SystemExit(main())
