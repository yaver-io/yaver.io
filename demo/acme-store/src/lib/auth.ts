interface LoginResult {
  success: boolean;
  token?: string;
  error?: string;
}

class AuthService {
  private baseUrl = 'https://api.acme-store.dev';

  async login(email: string, password: string): Promise<LoginResult> {
    try {
      const response = await fetch(`${this.baseUrl}/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        return { success: false, error: data.message || `HTTP ${response.status}` };
      }

      const data = await response.json();
      return { success: true, token: data.token };
    } catch (err: any) {
      return { success: false, error: err.message || 'Network error' };
    }
  }

  async logout(): Promise<void> {
    // Clear stored token
  }
}

export const authService = new AuthService();
