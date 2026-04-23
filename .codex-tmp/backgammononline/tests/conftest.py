"""
conftest.py — Shared Selenium fixtures for all tests.
"""
import pytest
from selenium import webdriver
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.chrome.service import Service
from webdriver_manager.chrome import ChromeDriverManager

BASE_URL = "http://localhost:5173"


@pytest.fixture(scope="session")
def driver():
    """Session-scoped Chrome WebDriver (headless)."""
    options = Options()
    options.add_argument("--headless=new")
    options.add_argument("--no-sandbox")
    options.add_argument("--disable-dev-shm-usage")
    options.add_argument("--window-size=1280,800")
    options.add_argument("--disable-gpu")

    service = Service(ChromeDriverManager().install())
    driver = webdriver.Chrome(service=service, options=options)
    driver.implicitly_wait(5)
    yield driver
    driver.quit()


@pytest.fixture(autouse=True)
def reset_to_home(driver):
    """Navigate to home before every test."""
    driver.get(BASE_URL)
    import time; time.sleep(0.5)
