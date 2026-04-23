"""
test_game_board.py — Tests for the Game Board UI.
Covers: board renders, player badges, dice, controls, point numbers.
"""
import time
import pytest
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC

BASE_URL = "http://localhost:5173"
GAME_URL = f"{BASE_URL}/play"


def go_to_game(driver):
    driver.get(GAME_URL)
    time.sleep(1)  # wait for React to render


class TestGameBoard:

    def test_board_renders(self, driver):
        """Board container must be rendered on /play."""
        go_to_game(driver)
        # Board is a div with a specific aspect ratio / background
        board = driver.find_element(By.XPATH, "//div[contains(@style, 'aspect-ratio')]")
        assert board.is_displayed()

    def test_point_numbers_visible(self, driver):
        """Point number labels 1-24 should be visible on the board."""
        go_to_game(driver)
        # Check a sample of point labels
        for num in [1, 6, 13, 19, 24]:
            els = driver.find_elements(By.XPATH, f"//*[text()='{num}']")
            assert len(els) > 0, f"Point number {num} not found"

    def test_bar_label_visible(self, driver):
        """BAR label should appear in the middle of the board."""
        go_to_game(driver)
        bar = driver.find_element(By.XPATH, "//*[contains(text(), 'BAR')]")
        assert bar.is_displayed()

    def test_white_player_badge_visible(self, driver):
        """User player card should be visible."""
        go_to_game(driver)
        badge = driver.find_element(By.XPATH, "//*[contains(text(), 'You')]")
        assert badge.is_displayed()

    def test_black_player_badge_visible(self, driver):
        """Opponent player card should be visible."""
        go_to_game(driver)
        badge = driver.find_element(By.XPATH, "//*[contains(text(), 'Player 2')]")
        assert badge.is_displayed()

    def test_borne_off_counters_start_zero(self, driver):
        """Both players should start with 0 / 15 borne-off checkers."""
        go_to_game(driver)
        counters = driver.find_elements(By.XPATH, "//*[contains(normalize-space(), '0 / 15')]")
        assert len(counters) >= 2, "Borne-off counter not showing 0 / 15 for both players"

    def test_score_display_visible(self, driver):
        """Score display is now handled by the borne off counters in the player cards."""
        pass

    def test_doubling_cube_visible(self, driver):
        """Doubling cube showing value '1' and 'CUBE' label should be visible."""
        go_to_game(driver)
        cube_label = driver.find_element(By.XPATH, "//*[contains(text(), 'CUBE')]")
        assert cube_label.is_displayed()

    def test_roll_dice_button_visible(self, driver):
        """'Roll Dice' button should be visible at game start (white's turn)."""
        go_to_game(driver)
        roll_btn = driver.find_element(By.XPATH, "//*[contains(text(), 'Roll Dice')]")
        assert roll_btn.is_displayed()

    def test_turn_indicator_shows_white(self, driver):
        """Turn indicator should show YOUR TURN on User card at game start."""
        go_to_game(driver)
        turn = driver.find_element(By.XPATH, "//*[contains(text(), 'YOUR TURN')]")
        assert turn.is_displayed()

    def test_new_game_button_visible(self, driver):
        """'New Game' button should always be visible."""
        go_to_game(driver)
        btn = driver.find_element(By.XPATH, "//*[contains(text(), 'New Game')]")
        assert btn.is_displayed()

    def test_back_to_menu_button_visible(self, driver):
        """'← Menu' back button must be visible on game page."""
        go_to_game(driver)
        back = driver.find_element(By.XPATH, "//*[contains(text(), '← Menu')]")
        assert back.is_displayed()

    def test_menu_nav_goes_home(self, driver):
        """Clicking '← Menu' navigates back to landing page."""
        go_to_game(driver)
        back = driver.find_element(By.XPATH, "//*[contains(text(), '← Menu')]")
        back.click()
        WebDriverWait(driver, 5).until(EC.url_to_be(BASE_URL + "/"))
        assert driver.current_url.rstrip("/") == BASE_URL

    def test_active_badge_on_white(self, driver):
        """White player badge should show 'YOUR TURN' indicator at game start."""
        go_to_game(driver)
        active = driver.find_element(By.XPATH, "//*[contains(text(), 'YOUR TURN')]")
        assert active.is_displayed()

    def test_header_shows_vs_ai(self, driver):
        """Game header should indicate 'VS AI' mode on /play."""
        go_to_game(driver)
        header = driver.find_element(By.XPATH, "//*[contains(text(), 'VS AI')]")
        assert header.is_displayed()

    def test_no_js_errors_on_game_load(self, driver):
        """No severe JS errors on game page load."""
        go_to_game(driver)
        logs = driver.get_log("browser")
        severe = [l for l in logs if l.get("level") == "SEVERE"]
        assert len(severe) == 0, f"JS errors on game page: {severe}"
