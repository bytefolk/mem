import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, apiBlob, apiRawResponse, setBrowserCSRF } from './api';

beforeEach(() => {
  localStorage.clear();
  setBrowserCSRF(null);
  window.history.replaceState({}, '', '/login');
});
afterEach(() => vi.unstubAllGlobals());

describe('session generation on pending requests', () => {
  it.each(['json', 'raw', 'blob'] as const)(
    'does not let an old %s 401 clear a new cookie login',
    async (kind) => {
      localStorage.setItem('mem.token', 'account-a-token');
      let finish!: (response: Response) => void;
      vi.stubGlobal(
        'fetch',
        vi.fn(
          () =>
            new Promise<Response>((resolve) => {
              finish = resolve;
            }),
        ),
      );
      const request =
        kind === 'json'
          ? api.get('/files')
          : kind === 'raw'
            ? apiRawResponse('/files')
            : apiBlob('/files/example/content');
      // Attach rejection handling before resolving the asynchronous old response.
      const result = request.catch(() => null);
      localStorage.removeItem('mem.token');
      localStorage.setItem('mem.auth.cookie', 'new-b-session');
      localStorage.setItem('mem.user', JSON.stringify({ id: 'account-b' }));
      setBrowserCSRF('new-b-csrf');
      finish(new Response(JSON.stringify({ error: 'session_expired' }), { status: 401 }));
      await result;
      expect(localStorage.getItem('mem.auth.cookie')).toBe('new-b-session');
      expect(localStorage.getItem('mem.user')).toContain('account-b');
    },
  );
});
