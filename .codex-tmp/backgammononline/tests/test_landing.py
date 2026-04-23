"""
test_landing.py — Tests for the Landing Page.
Covers: title, hero text, CTAs, feature cards, stack pills, navigation.
"""
import pytest
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC

BASE_URL = "http://localhost:5173"


class TestLandingPage:

    def test_page_title(self, driver):
        """Page title should contain 'Backgammon'."""
        assert "Backgammon" in driver.title

    def test_logo_visible(self, driver):
        """Logo text 'BACKGAMMON' should be visible in nav."""
        logo = driver.find_element(By.XPATH, "//*[contains(text(), 'BACKGAMMON')]")
        assert logo.is_displayed()

    def test_phase_badge_visible(self, driver):
        """Phase 1 MVP badge should be visible in nav."""
        badge = driver.find_element(By.XPATH, "//*[contains(text(), 'Phase 1 MVP')]")
        assert badge.is_displayed()

    def test_hero_headline_present(self, driver):
        """Hero section must contain the main headline."""
        headline = driver.find_element(By.TAG_NAME, "h1")
        assert "backgammon" in headline.text.lower()

    def test_live_badge_visible(self, driver):
        """'Now live' badge should be visible."""
        badge = driver.find_element(By.XPATH, "//*[contains(text(), 'Now live')]")
        assert badge.is_displayed()

    def test_play_vs_ai_button_visible(self, driver):
        """'Play vs AI' CTA button must be visible."""
        btn = driver.find_element(By.XPATH, "//button[contains(text(), 'Play vs AI')]")
        assert btn.is_displayed()

    def test_local_2player_button_visible(self, driver):
        """'Local 2-Player' CTA button must be visible."""
        btn = driver.find_element(By.XPATH, "//button[contains(text(), 'Local 2-Player')]")
        assert btn.is_displayed()

    def test_feature_cards_count(self, driver):
        """Four feature cards should be visible."""
        features = ["AI Opponent", "Local 2-Player", "Tournaments", "Real-Time"]
        for label in features:
            el = WebDriverWait(driver, 5).until(
                EC.visibility_of_element_located((By.XPATH, f"//*[contains(text(), '{label}')]"))
            )
            assert el.is_displayed(), f"Feature card '{label}' not visible"

    def test_stack_pills_visible(self, driver):
        """Tech stack pills should be displayed."""
        stacks = ["React + Vite", "Convex", "Tailwind CSS"]
        for s in stacks:
            el = WebDriverWait(driver, 5).until(
                EC.visibility_of_element_located((By.XPATH, f"//*[contains(text(), '{s}')]"))
            )
            assert el.is_displayed(), f"Stack pill '{s}' not visible"

    def test_footer_visible(self, driver):
        """Footer with 'CarrotByte' should be visible."""
        footer = WebDriverWait(driver, 5).until(
            EC.visibility_of_element_located((By.XPATH, "//*[contains(text(), 'CarrotByte')]"))
        )
        assert footer.is_displayed()

    def test_mini_board_preview_visible(self, driver):
        """Mini board preview (decorative) should be rendered."""
        # The preview is a styled div — check it exists via its unique style
        # We check that the page has more than one triangle-shaped element (clip-path divs)
        triangles = driver.find_elements(By.CSS_SELECTOR, "div[style*='polygon']")
        assert len(triangles) > 0, "No triangle elements found in board preview"

    def test_navigate_to_ai_game(self, driver):
        """Clicking 'Play vs AI' navigates to /play."""
        btn = driver.find_element(By.XPATH, "//button[contains(text(), 'Play vs AI')]")
        btn.click()
        WebDriverWait(driver, 5).until(EC.url_contains("/play"))
        assert "/play" in driver.current_url

    def test_navigate_to_local_game(self, driver):
        """Clicking 'Local 2-Player' navigates to /local."""
        btn = driver.find_element(By.XPATH, "//button[contains(text(), 'Local 2-Player')]")
        btn.click()
        WebDriverWait(driver, 5).until(EC.url_contains("/local"))
        assert "/local" in driver.current_url

    def test_no_js_errors_on_load(self, driver):
        """No severe JS errors should appear on page load."""
        logs = driver.get_log("browser")
        severe = [l for l in logs if l.get("level") == "SEVERE"]
        assert len(severe) == 0, f"JS errors on landing: {severe}"
