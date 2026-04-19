/**
 * LoginModal — vanilla-DOM sign-in modal for `yaver-feedback-web`.
 *
 * Compact, framework-agnostic (no React/Vue/etc. peer dep). Mirrors the
 * yaver-feedback-react-native 0.6+ login UX: five OAuth providers via popup
 * sign-in (Apple / Google / GitHub / GitLab / Microsoft), plus collapsible
 * email + password. Designed to feel like an embedded utility widget rather
 * than a primary auth screen.
 *
 * @example
 * ```ts
 * const token = await LoginModal.open();
 * ```
 */

import {
  loginWithEmail,
  saveToken,
  saveUser,
  signInWithOAuth,
  signupWithEmail,
  validateToken,
  type OAuthProvider,
} from './auth';

const STYLE_ID = 'yaver-feedback-login-style';

const PROVIDERS: { id: OAuthProvider; label: string }[] = [
  { id: 'apple', label: 'Continue with Apple' },
  { id: 'google', label: 'Continue with Google' },
  { id: 'github', label: 'Continue with GitHub' },
  { id: 'gitlab', label: 'Continue with GitLab' },
  { id: 'microsoft', label: 'Continue with Microsoft' },
];

const CSS = `
.yvr-fb-login-overlay {
  position: fixed;
  inset: 0;
  z-index: 2147483646;
  background: rgba(10, 10, 18, 0.65);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: #e0e0e0;
}
.yvr-fb-login-card {
  background: #1a1a2e;
  border: 1px solid rgba(255,255,255,0.08);
  border-radius: 14px;
  width: 100%;
  max-width: 360px;
  padding: 22px 22px 18px;
  box-shadow: 0 24px 60px rgba(0,0,0,0.45);
}
.yvr-fb-login-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 14px;
}
.yvr-fb-login-title {
  font-size: 15px;
  font-weight: 700;
  color: #e0e0e0;
}
.yvr-fb-login-sub {
  font-size: 12px;
  color: #9ca3af;
  margin-top: 2px;
}
.yvr-fb-login-cancel {
  background: none;
  border: none;
  color: #9ca3af;
  font-size: 13px;
  cursor: pointer;
  padding: 4px 6px;
}
.yvr-fb-login-cancel:hover { color: #e0e0e0; }
.yvr-fb-login-buttons { display: flex; flex-direction: column; gap: 8px; }
.yvr-fb-login-btn {
  background: rgba(255,255,255,0.05);
  border: 1px solid rgba(255,255,255,0.1);
  color: #e0e0e0;
  font-size: 13px;
  font-weight: 600;
  padding: 10px 12px;
  border-radius: 9px;
  cursor: pointer;
  text-align: center;
  font-family: inherit;
  transition: background 0.12s;
}
.yvr-fb-login-btn:hover { background: rgba(255,255,255,0.1); }
.yvr-fb-login-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.yvr-fb-login-btn-primary {
  background: #6366f1;
  border-color: #6366f1;
  color: #fff;
}
.yvr-fb-login-btn-primary:hover { background: #818cf8; }
.yvr-fb-login-divider {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 12px 0 6px;
  color: #6b7280;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.6px;
}
.yvr-fb-login-divider::before,
.yvr-fb-login-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: rgba(255,255,255,0.08);
}
.yvr-fb-login-input {
  background: rgba(255,255,255,0.05);
  border: 1px solid rgba(255,255,255,0.1);
  color: #e0e0e0;
  font-size: 13px;
  padding: 10px 12px;
  border-radius: 9px;
  width: 100%;
  box-sizing: border-box;
  font-family: inherit;
}
.yvr-fb-login-input:focus {
  outline: none;
  border-color: #6366f1;
}
.yvr-fb-login-error {
  color: #ef4444;
  font-size: 12px;
  text-align: center;
  margin: 4px 0 0;
}
.yvr-fb-login-toggle {
  background: none;
  border: none;
  color: #818cf8;
  font-size: 12px;
  cursor: pointer;
  text-align: center;
  margin-top: 4px;
  padding: 4px;
  font-family: inherit;
}
.yvr-fb-login-toggle:hover { color: #a5b4fc; }
.yvr-fb-login-spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid rgba(255,255,255,0.25);
  border-top-color: #fff;
  border-radius: 50%;
  animation: yvr-fb-spin 0.7s linear infinite;
  vertical-align: middle;
}
@keyframes yvr-fb-spin { to { transform: rotate(360deg); } }
`;

function injectStyles(): void {
  if (typeof document === 'undefined') return;
  if (document.getElementById(STYLE_ID)) return;
  const style = document.createElement('style');
  style.id = STYLE_ID;
  style.textContent = CSS;
  document.head.appendChild(style);
}

export interface LoginModalOptions {
  /** Custom title — defaults to "Sign in to send feedback". */
  title?: string;
  /** Custom subtitle. */
  subtitle?: string;
  /** Allow user to dismiss the modal. Default true. Throws `cancelled` if dismissed. */
  cancellable?: boolean;
}

/**
 * Open the SDK login modal. Resolves with the issued session token once the
 * user authenticates and saves it to localStorage. Rejects with `cancelled`
 * if the user dismisses the modal.
 */
export function openLoginModal(opts: LoginModalOptions = {}): Promise<string> {
  if (typeof document === 'undefined') {
    return Promise.reject(new Error('LoginModal requires a browser'));
  }
  injectStyles();

  return new Promise<string>((resolve, reject) => {
    const overlay = document.createElement('div');
    overlay.className = 'yvr-fb-login-overlay';

    const card = document.createElement('div');
    card.className = 'yvr-fb-login-card';
    overlay.appendChild(card);

    let busy = false;
    let showEmail = false;
    let isSignUp = false;

    const close = () => {
      overlay.remove();
    };

    const finish = async (token: string) => {
      try {
        const user = await validateToken(token);
        saveToken(token);
        if (user) saveUser(user);
        close();
        resolve(token);
      } catch (err) {
        renderError(err instanceof Error ? err.message : String(err));
      }
    };

    const renderError = (msg: string) => {
      const existing = card.querySelector('.yvr-fb-login-error');
      if (existing) existing.textContent = msg;
    };

    const setBusy = (state: boolean, label?: string) => {
      busy = state;
      card.querySelectorAll<HTMLButtonElement>('button').forEach((b) => {
        b.disabled = state;
      });
      if (state && label) {
        const target = card.querySelector<HTMLButtonElement>(`[data-label="${label}"]`);
        if (target) target.innerHTML = '<span class="yvr-fb-login-spinner"></span>';
      }
    };

    const handleOAuth = async (provider: OAuthProvider) => {
      if (busy) return;
      setBusy(true, provider);
      try {
        const { token } = await signInWithOAuth(provider);
        await finish(token);
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Sign-in failed';
        if (msg !== 'cancelled') {
          renderError(
            msg === 'popup_blocked'
              ? 'Browser blocked the sign-in popup — please allow popups for this page.'
              : msg,
          );
        }
        render();
      }
    };

    const handleEmailSubmit = async (
      fullName: string,
      email: string,
      password: string,
      confirmPassword: string,
    ) => {
      if (busy) return;
      if (isSignUp) {
        if (!fullName.trim()) return renderError('Full name is required');
        if (password !== confirmPassword)
          return renderError('Passwords do not match');
        if (password.length < 8)
          return renderError('Password must be at least 8 characters');
      }
      if (!email.trim() || !password)
        return renderError('Email and password are required');

      setBusy(true, 'email-submit');
      try {
        const result = isSignUp
          ? await signupWithEmail(fullName.trim(), email.trim(), password)
          : await loginWithEmail(email.trim(), password);
        await finish(result.token);
      } catch (err) {
        renderError(err instanceof Error ? err.message : 'Something went wrong');
        render();
      }
    };

    const render = () => {
      card.innerHTML = '';

      const header = document.createElement('div');
      header.className = 'yvr-fb-login-header';
      const titles = document.createElement('div');
      const title = document.createElement('div');
      title.className = 'yvr-fb-login-title';
      title.textContent = opts.title ?? 'Sign in to send feedback';
      const sub = document.createElement('div');
      sub.className = 'yvr-fb-login-sub';
      sub.textContent =
        opts.subtitle ?? 'Yaver routes your bug report to your dev machine.';
      titles.appendChild(title);
      titles.appendChild(sub);
      header.appendChild(titles);

      if (opts.cancellable !== false) {
        const cancel = document.createElement('button');
        cancel.className = 'yvr-fb-login-cancel';
        cancel.textContent = 'Cancel';
        cancel.onclick = () => {
          close();
          reject(new Error('cancelled'));
        };
        header.appendChild(cancel);
      }
      card.appendChild(header);

      const buttons = document.createElement('div');
      buttons.className = 'yvr-fb-login-buttons';

      for (const p of PROVIDERS) {
        const btn = document.createElement('button');
        btn.className = 'yvr-fb-login-btn';
        btn.dataset.label = p.id;
        btn.textContent = p.label;
        btn.onclick = () => handleOAuth(p.id);
        buttons.appendChild(btn);
      }

      if (!showEmail) {
        const emailBtn = document.createElement('button');
        emailBtn.className = 'yvr-fb-login-btn';
        emailBtn.textContent = 'Continue with Email';
        emailBtn.onclick = () => {
          showEmail = true;
          render();
        };
        buttons.appendChild(emailBtn);
      } else {
        const divider = document.createElement('div');
        divider.className = 'yvr-fb-login-divider';
        divider.textContent = 'email';
        buttons.appendChild(divider);

        let nameInput: HTMLInputElement | null = null;
        if (isSignUp) {
          nameInput = document.createElement('input');
          nameInput.className = 'yvr-fb-login-input';
          nameInput.placeholder = 'Full Name';
          nameInput.autocomplete = 'name';
          buttons.appendChild(nameInput);
        }

        const emailInput = document.createElement('input');
        emailInput.className = 'yvr-fb-login-input';
        emailInput.placeholder = 'Email';
        emailInput.type = 'email';
        emailInput.autocomplete = 'email';
        buttons.appendChild(emailInput);

        const passInput = document.createElement('input');
        passInput.className = 'yvr-fb-login-input';
        passInput.placeholder = 'Password';
        passInput.type = 'password';
        passInput.autocomplete = isSignUp ? 'new-password' : 'current-password';
        buttons.appendChild(passInput);

        let confirmInput: HTMLInputElement | null = null;
        if (isSignUp) {
          confirmInput = document.createElement('input');
          confirmInput.className = 'yvr-fb-login-input';
          confirmInput.placeholder = 'Confirm Password';
          confirmInput.type = 'password';
          confirmInput.autocomplete = 'new-password';
          buttons.appendChild(confirmInput);
        }

        const errorEl = document.createElement('p');
        errorEl.className = 'yvr-fb-login-error';
        errorEl.textContent = '';
        buttons.appendChild(errorEl);

        const submit = document.createElement('button');
        submit.className = 'yvr-fb-login-btn yvr-fb-login-btn-primary';
        submit.dataset.label = 'email-submit';
        submit.textContent = isSignUp ? 'Create Account' : 'Sign In';
        submit.onclick = () =>
          handleEmailSubmit(
            nameInput?.value ?? '',
            emailInput.value,
            passInput.value,
            confirmInput?.value ?? '',
          );
        buttons.appendChild(submit);

        const toggle = document.createElement('button');
        toggle.className = 'yvr-fb-login-toggle';
        toggle.textContent = isSignUp
          ? 'Already have an account? Sign in'
          : "Don't have an account? Sign up";
        toggle.onclick = () => {
          isSignUp = !isSignUp;
          render();
        };
        buttons.appendChild(toggle);
      }

      card.appendChild(buttons);
    };

    render();
    document.body.appendChild(overlay);
  });
}

export const LoginModal = { open: openLoginModal };
