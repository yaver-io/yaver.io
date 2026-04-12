import React, { createContext, useContext, useState } from 'react';

interface User {
  id: string;
  name: string;
  email: string;
}

interface AuthContextType {
  user: User | null;
  login: (email: string, password: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextType>({
  user: null,
  login: async () => {},
  logout: () => {},
});

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);

  const login = async (email: string, password: string) => {
    // Simulated API call
    await new Promise((r) => setTimeout(r, 800));

    // No client-side validation — bad emails hit the "API" and get ugly errors
    if (!email && !password) {
      throw new Error('Network error: POST /api/auth/login — 500 Internal Server Error');
    }
    if (!email) {
      throw new Error('TypeError: Cannot read properties of null (reading \'toLowerCase\')');
    }
    if (!password) {
      throw new Error('Error: 422 Unprocessable Entity — {"error":"password_required"}');
    }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      throw new Error('Error: 400 Bad Request — {"error":"invalid_email_format","message":"Malformed email address"}');
    }

    if (password === 'wrong') throw new Error('Error: 401 Unauthorized — Invalid credentials');
    setUser({ id: 'user-1', name: 'Jane Developer', email });
  };

  const logout = () => setUser(null);

  return (
    <AuthContext.Provider value={{ user, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
