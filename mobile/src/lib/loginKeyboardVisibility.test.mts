// loginKeyboardVisibility.test.mts — the email/password login form must keep
// both the focused field and the next control above the software keyboard.
//
// The iOS ScrollView's automaticallyAdjustKeyboardInsets keeps the first
// responder visible, but it does not reserve room for the password field below
// a focused email field. That left Password fully covered on the sign-in screen
// (reported 2026-08-30). This structural test pins the native scroll-responder
// hook and the email -> password focus path without mounting the full Expo tree.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const login = readFileSync(join(here, "../../app/login.tsx"), "utf8");
const emailLogin = readFileSync(join(here, "../../app/email-login.tsx"), "utf8");

function inputWithTestID(testID: string): string {
  const marker = `testID="${testID}"`;
  const markerIndex = login.indexOf(marker);
  assert.ok(markerIndex >= 0, `${testID} must exist`);
  const inputStart = login.lastIndexOf("<TextInput", markerIndex);
  const inputEnd = login.indexOf("/>", markerIndex);
  assert.ok(inputStart >= 0 && inputEnd >= 0, `${testID} must belong to a TextInput`);
  return login.slice(inputStart, inputEnd + 2);
}

test("email sign-in reserves keyboard room for the following control", () => {
  assert.match(login, /const loginScrollRef = useRef<ScrollView>\(null\)/);
  assert.match(login, /ref=\{loginScrollRef\}/);
  assert.match(login, /scrollResponderScrollNativeHandleToKeyboard/);
  assert.match(login, /LOGIN_FOLLOWING_CONTROL_CLEARANCE/);
  assert.match(login, /if \(Platform\.OS === "web"\) return/);

  const emailInput = inputWithTestID("login-email-input");
  assert.match(emailInput, /onFocus=\{keepNextLoginControlVisible\}/);
  assert.match(emailInput, /onSubmitEditing=\{focusPassword\}/);
  assert.match(emailInput, /returnKeyType="next"/);

  const passwordInput = inputWithTestID("login-password-input");
  assert.match(passwordInput, /ref=\{passwordInputRef\}/);
  assert.match(passwordInput, /onFocus=\{keepNextLoginControlVisible\}/);
  assert.match(login, /testID="login-email-submit"/);
});

test("the platform still owns the actual keyboard inset", () => {
  assert.match(login, /automaticallyAdjustKeyboardInsets/);
  assert.match(login, /keyboardDismissMode="interactive"/);
  assert.match(login, /Keyboard\.addListener\("keyboardDidShow"/);
  assert.match(login, /showEmailForm && keyboardVisible && styles\.scrollContainerKeyboard/);
  assert.match(login, /showEmailForm && styles\.hidden/);
  assert.doesNotMatch(login, /<KeyboardAvoidingView/);
});

test("the non-OAuth stage routes email login to a dedicated two-field page", () => {
  assert.match(
    login,
    /const \[loginPane, setLoginPane\] = useState<"email" \| "options">\("options"\)/,
  );
  assert.match(login, /testID="login-email-entry"/);
  assert.match(login, /router\.push\("\/email-login"\)/);
  assert.match(login, />Sign in with email<\/Text>/);
  assert.ok(
    login.indexOf('testID="login-email-entry"') < login.indexOf("{passkeySupported && !showEmailForm"),
    "email login must appear before passkey and OAuth choices",
  );
  assert.match(login, /\{!showEmailForm && <View style=\{\[styles\.providerGroup/);

  assert.equal((emailLogin.match(/\n\s*<TextInput\n/g) ?? []).length, 2);
  assert.match(emailLogin, /testID="email-login-email"/);
  assert.match(emailLogin, /testID="email-login-password"/);
  assert.match(emailLogin, /testID="email-login-submit"/);
  assert.match(emailLogin, /automaticallyAdjustKeyboardInsets/);
  assert.match(emailLogin, /scrollResponderScrollNativeHandleToKeyboard/);

  const emailInput = inputWithTestID("login-email-input");
  const passwordInput = inputWithTestID("login-password-input");
  assert.match(emailInput, /placeholder="Email"/);
  assert.match(passwordInput, /placeholder="Password"/);
  assert.match(login, /setLoginPane\("options"\)/);
});
