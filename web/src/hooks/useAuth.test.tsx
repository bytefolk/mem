import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthProvider, useAuth } from './useAuth';
import { api, getToken, setBrowserCSRF } from '@/lib/api';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { I18nProvider } from '@/i18n';
import { LoginPage } from '@/pages/LoginPage';

const user = { id: 'personal-user', email: 'owner@example.test' };
const session = (id: string, csrf: string) => ({
  authenticated: true,
  user,
  session_id: id,
  csrf_token: csrf,
});
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
let current: ReturnType<typeof useAuth>;
function Probe() {
  current = useAuth();
  return <span>{current.loading ? 'loading' : (current.user?.email ?? 'signed-out')}</span>;
}
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Probe />
      </AuthProvider>
    </QueryClientProvider>,
  );
  return client;
}

beforeEach(() => {
  localStorage.clear();
  setBrowserCSRF(null);
  window.history.replaceState({}, '', '/login');
});
afterEach(() => vi.unstubAllGlobals());

describe('GitHub browser session', () => {
  it.each(['missing', 'network'] as const)(
    'never falls back to a stale Bearer when callback session is %s',
    async (failure) => {
      window.history.replaceState({}, '', '/login?github=ok');
      localStorage.setItem('mem.token', 'stale-account-bearer');
      localStorage.setItem('mem.user', JSON.stringify(user));
      const fetcher = vi.fn();
      if (failure === 'missing') fetcher.mockResolvedValue(response({ authenticated: false }));
      else fetcher.mockRejectedValue(new Error('network unavailable'));
      vi.stubGlobal('fetch', fetcher);
      mount();
      await screen.findByText('signed-out');
      expect(current.token).toBeNull();
      expect(getToken()).toBeNull();
      expect((fetcher.mock.calls[0]?.[1] as RequestInit).headers).not.toHaveProperty('Authorization');
    },
  );
  it('waits on the actual login route before replacing a stale local Bearer', async () => {
    window.history.replaceState({}, '', '/login?github=ok');
    localStorage.setItem('mem.token', 'stale-account-bearer');
    let finishSession!: (value: Response) => void;
    const fetcher = vi.fn().mockImplementation((url: string) =>
      url.endsWith('/auth/session')
        ? new Promise<Response>((resolve) => {
            finishSession = resolve;
          })
        : Promise.resolve(response({ github: true, registration: false })),
    );
    vi.stubGlobal('fetch', fetcher);
    const router = createMemoryRouter(
      [
        { path: '/login', element: <LoginPage /> },
        { path: '/', element: <Probe /> },
      ],
      { initialEntries: ['/login?github=ok'] },
    );
    render(
      <QueryClientProvider client={new QueryClient()}>
        <I18nProvider>
          <AuthProvider>
            <RouterProvider router={router} />
          </AuthProvider>
        </I18nProvider>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(finishSession).toBeDefined());
    expect(router.state.location.pathname).toBe('/login');
    expect(screen.getByRole('status')).toHaveTextContent('…');
    await act(async () => finishSession(response(session('fresh-session', 'fresh-csrf'))));
    await waitFor(() => expect(router.state.location.pathname).toBe('/'));
    expect(getToken()).toBeNull();
    expect(localStorage.getItem('mem.auth.cookie')).toBe('fresh-session');
    expect(current.user?.id).toBe(user.id);
  });
  it('bootstraps the callback cookie and sends CSRF without minting or storing a Bearer', async () => {
    window.history.replaceState({}, '', '/login?github=ok');
    localStorage.setItem('mem.token', 'old-local-token');
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response(session('session-1', 'csrf-1')))
      .mockResolvedValue(response({ ok: true }));
    vi.stubGlobal('fetch', fetcher);
    mount();
    await screen.findByText(user.email);
    expect(current.token).toBe('cookie-session');
    expect(getToken()).toBeNull();
    expect(localStorage.getItem('mem.auth.cookie')).toBe('session-1');
    await api.post('/memories', { content: 'owned' });
    const options = fetcher.mock.calls[1]?.[1] as RequestInit;
    expect(options.credentials).toBe('same-origin');
    expect(options.headers).toMatchObject({ 'X-Mem-CSRF': 'csrf-1' });
    expect(options.headers).not.toHaveProperty('Authorization');
    expect(localStorage.getItem('mem.auth.cookie')).not.toContain('csrf');
  });

  it('refreshes CSRF and clears cached data when another tab replaces the same user session', async () => {
    localStorage.setItem('mem.auth.cookie', 'session-1');
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response(session('session-1', 'csrf-1')))
      .mockResolvedValueOnce(response(session('session-2', 'csrf-2')))
      .mockResolvedValue(response({ ok: true }));
    vi.stubGlobal('fetch', fetcher);
    const client = mount();
    await screen.findByText(user.email);
    client.setQueryData(['private-file'], 'old-account-cache');
    localStorage.setItem('mem.auth.cookie', 'session-2');
    act(() =>
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'mem.auth.cookie',
          oldValue: 'session-1',
          newValue: 'session-2',
        }),
      ),
    );
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    await screen.findByText(user.email);
    expect(client.getQueryData(['private-file'])).toBeUndefined();
    await api.post('/memories', {});
    expect((fetcher.mock.calls[2]?.[1] as RequestInit).headers).toMatchObject({
      'X-Mem-CSRF': 'csrf-2',
    });
  });

  it('keeps the browser session visible until server logout acknowledges revocation', async () => {
    localStorage.setItem('mem.auth.cookie', 'session-1');
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response(session('session-1', 'csrf-1')))
      .mockResolvedValueOnce(response({ error: 'logout_unavailable' }, 503))
      .mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal('fetch', fetcher);
    mount();
    await screen.findByText(user.email);
    await act(async () => {
      await expect(current.logout()).rejects.toThrow('logout_unavailable');
    });
    expect(current.user?.id).toBe(user.id);
    expect(localStorage.getItem('mem.auth.cookie')).toBe('session-1');
    await act(async () => current.logout());
    expect(current.user).toBeNull();
    expect(localStorage.getItem('mem.auth.cookie')).toBeNull();
    expect(current.loading).toBe(false);
  });

  it('ignores an old refresh response that arrives after logout', async () => {
    localStorage.setItem('mem.auth.cookie', 'session-1');
    let finishRefresh!: (value: Response) => void;
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(response(session('session-1', 'csrf-1')))
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finishRefresh = resolve;
          }),
      )
      .mockResolvedValueOnce(response({ ok: true }));
    vi.stubGlobal('fetch', fetcher);
    mount();
    await screen.findByText(user.email);
    act(() => window.dispatchEvent(new StorageEvent('storage', { key: 'mem.auth.cookie' })));
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    await act(async () => current.logout());
    await act(async () => finishRefresh(response(session('old-response', 'old-csrf'))));
    expect(current.user).toBeNull();
    expect(localStorage.getItem('mem.auth.cookie')).toBeNull();
  });
});
