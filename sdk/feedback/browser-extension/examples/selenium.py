"""
Drive the Yaver browser extension from Selenium.

Loads the unpacked extension, navigates to a URL, then calls
window.__yaver.capturePage() — the result lands on your local Yaver agent
as a `design-reference` feedback bundle.

    python selenium.py https://stripe.com/payments
"""

import os
import sys
import time
from pathlib import Path

from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.common.exceptions import JavascriptException


EXT_DIR = Path(__file__).resolve().parent.parent


def make_driver(headless: bool = False) -> webdriver.Chrome:
    opts = Options()
    opts.add_argument(f"--load-extension={EXT_DIR}")
    opts.add_argument("--no-first-run")
    opts.add_argument("--no-default-browser-check")
    if headless:
        # Classic --headless drops extensions; --headless=new keeps them.
        opts.add_argument("--headless=new")
    return webdriver.Chrome(options=opts)


def wait_for_yaver(driver, timeout: float = 5.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if driver.execute_script("return !!window.__yaver"):
            return
        time.sleep(0.1)
    raise RuntimeError("window.__yaver never appeared — content script did not load")


def capture(driver, url: str, selector: str | None = None, full_page: bool = False):
    driver.get(url)
    wait_for_yaver(driver)
    if selector:
        return driver.execute_async_script(
            "const cb = arguments[arguments.length - 1];"
            "window.__yaver.captureSelector(arguments[0]).then(cb, (e) => cb({ok:false, error:String(e)}));",
            selector,
        )
    if full_page:
        return driver.execute_async_script(
            "const cb = arguments[arguments.length - 1];"
            "window.__yaver.captureFullPage().then(cb, (e) => cb({ok:false, error:String(e)}));"
        )
    return driver.execute_async_script(
        "const cb = arguments[arguments.length - 1];"
        "window.__yaver.capturePage().then(cb, (e) => cb({ok:false, error:String(e)}));"
    )


def main():
    url = sys.argv[1] if len(sys.argv) > 1 else "https://example.com"
    selector = os.environ.get("YAVER_SELECTOR")
    headless = os.environ.get("YAVER_HEADLESS") == "1"

    driver = make_driver(headless=headless)
    try:
        result = capture(driver, url, selector=selector, full_page=not selector)
        if not result.get("ok"):
            print(f"capture failed: {result}", file=sys.stderr)
            sys.exit(1)
        nodes = len((result.get("bundle") or {}).get("styles", {}).get("nodes") or [])
        sent = result.get("sent")
        print(f"captured {url} · {nodes} nodes · sent_to_agent={sent}")
    except JavascriptException as e:
        print(f"page error: {e}", file=sys.stderr)
        sys.exit(2)
    finally:
        driver.quit()


if __name__ == "__main__":
    main()
