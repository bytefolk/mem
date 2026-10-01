import * as React from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { User, AuthLoginResponse } from '@/lib/types';
import {
  api,
  clearToken,
  getToken,
  setToken,
  setBrowserCSRF,
  setCurrentWorkspaceID,
} from '@/lib/api';

interface AuthContextValue {
  user: User | null;
  token: string | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthCtx = React.createContext<AuthContextValue | null>(null);
const USER_KEY = 'mem.user';
const COOKIE_KEY = 'mem.auth.cookie';
const cookieReturn = () =>
  ['ok', 'linked'].includes(new URLSearchParams(window.location.search).get('github') ?? '');

function readUser(): User | null {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as User;
  } catch {
    return null;
  }
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient();
  const callbackAtMount = React.useRef(cookieReturn()).current;
  const [token, setTokenState] = React.useState<string | null>(() => getToken());
  const [user, setUser] = React.useState<User | null>(() => readUser());
  const [loading, setLoading] = React.useState(
    () => callbackAtMount || !!localStorage.getItem(COOKIE_KEY),
  );
  const authGeneration = React.useRef(0);

  React.useEffect(() => {
    let active = true;
    const refresh = (initial = false) => {
      const current = ++authGeneration.current;
      if (!(initial && callbackAtMount) && !localStorage.getItem(COOKIE_KEY)) {
        setBrowserCSRF(null);
        setTokenState(getToken());
        setUser(readUser());
        setLoading(false);
        return;
      }
      // Once this browser chooses cookie auth, stale local Bearers must not
      // survive a missing/failed callback session or override its readback.
      clearToken();
      setBrowserCSRF(null);
      setTokenState(null);
      setUser(null);
      queryClient.clear();
      setLoading(true);
      api
        .get<{ authenticated: boolean; user?: User; csrf_token?: string; session_id?: string }>(
          '/auth/session',
        )
        .then((res) => {
          if (!active || current !== authGeneration.current) return;
          if (res.authenticated && res.user && res.csrf_token && res.session_id) {
            if (initial && callbackAtMount) setCurrentWorkspaceID(null);
            clearToken();
            setBrowserCSRF(res.csrf_token);
            localStorage.setItem(COOKIE_KEY, res.session_id);
            localStorage.setItem(USER_KEY, JSON.stringify(res.user));
            setTokenState('cookie-session'); // UI marker, never an authentication secret
            setUser(res.user);
          } else {
            localStorage.removeItem(COOKIE_KEY);
            setBrowserCSRF(null);
            setTokenState(getToken());
            if (!getToken()) {
              localStorage.removeItem(USER_KEY);
              setUser(null);
            }
          }
        })
        .catch(() => {
          if (active && current === authGeneration.current) {
            setTokenState(null);
            setUser(null);
          }
        })
        .finally(() => {
          if (active && current === authGeneration.current) setLoading(false);
        });
    };
    refresh(true);
    const sync = (event: StorageEvent) => {
      if ([COOKIE_KEY, USER_KEY].includes(event.key ?? '')) {
        queryClient.clear();
        refresh();
      }
    };
    window.addEventListener('storage', sync);
    return () => {
      active = false;
      window.removeEventListener('storage', sync);
    };
  }, [queryClient, callbackAtMount]);

  const persistSession = React.useCallback(
    (res: AuthLoginResponse) => {
      ++authGeneration.current;
      queryClient.clear();
      localStorage.removeItem(COOKIE_KEY);
      setBrowserCSRF(null);
      setCurrentWorkspaceID(null);
      setToken(res.token);
      localStorage.setItem(USER_KEY, JSON.stringify(res.user));
      setTokenState(res.token);
      setUser(res.user);
    },
    [queryClient],
  );

  const login = React.useCallback(
    async (email: string, password: string) => {
      setLoading(true);
      try {
        persistSession(await api.post<AuthLoginResponse>('/auth/login', { email, password }));
      } finally {
        setLoading(false);
      }
    },
    [persistSession],
  );

  const register = React.useCallback(
    async (email: string, password: string) => {
      setLoading(true);
      try {
        persistSession(await api.post<AuthLoginResponse>('/auth/register', { email, password }));
      } finally {
        setLoading(false);
      }
    },
    [persistSession],
  );

  const logout = React.useCallback(async () => {
    if (localStorage.getItem(COOKIE_KEY)) await api.post('/auth/logout');
    ++authGeneration.current;
    clearToken();
    localStorage.removeItem(COOKIE_KEY);
    setBrowserCSRF(null);
    setCurrentWorkspaceID(null);
    localStorage.removeItem(USER_KEY);
    setTokenState(null);
    setUser(null);
    setLoading(false);
    queryClient.clear();
  }, [queryClient]);

  const value = React.useMemo<AuthContextValue>(
    () => ({ user, token, loading, login, register, logout }),
    [user, token, loading, login, register, logout],
  );

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = React.useContext(AuthCtx);
  if (!ctx) throw new Error('useAuth must be used within <AuthProvider>');
  return ctx;
}
