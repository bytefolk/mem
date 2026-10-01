import * as React from 'react';
import { api, getToken, clearToken, setBrowserCSRF } from '@/lib/api';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { useT } from '@/i18n';

export function AccountPage() {
  const { t } = useT();
  const [enabled, setEnabled] = React.useState(false);
  const [identity, setIdentity] = React.useState<{ login: string } | null>(null);
  const [password, setPassword] = React.useState('');
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  React.useEffect(() => {
    api
      .get<{ github: boolean }>('/auth/capabilities')
      .then((value) => setEnabled(value.github))
      .catch(() => setError(t('github.failed')));
    api
      .get<{ github: { login: string } | null }>('/auth/github/identity')
      .then((value) => setIdentity(value.github))
      .catch(() => setError(t('github.failed')));
  }, [t]);
  async function link() {
    setBusy(true);
    setError('');
    try {
      const result = await api.post<{ url: string; csrf_token?: string; session_id?: string }>(
        '/auth/github/start',
        {
          intent: 'link',
          password,
        },
      );
      if (result.csrf_token) {
        clearToken();
        setBrowserCSRF(result.csrf_token);
        localStorage.setItem('mem.auth.cookie', result.session_id ?? crypto.randomUUID());
      }
      setPassword('');
      window.location.assign(result.url);
    } catch {
      setError(t('github.reauthenticate'));
      setBusy(false);
    }
  }
  return (
    <div className="mx-auto max-w-xl p-6">
      <h1 className="text-xl font-semibold mb-4">{t('nav.account')}</h1>
      <section className="surface p-5 flex flex-col gap-4">
        <h2 className="font-medium">GitHub</h2>
        {identity ? (
          <p>{t('github.connected', { login: identity.login })}</p>
        ) : enabled ? (
          <>
            <p className="text-sm text-fg-muted">{t('github.linkDescription')}</p>
            {getToken() && (
              <Input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                aria-label={t('login.password')}
                placeholder={t('login.password')}
              />
            )}
            <Button variant="primary" onClick={link} loading={busy}>
              {t('github.link')}
            </Button>
          </>
        ) : (
          <p>{t('github.disabled')}</p>
        )}
        {error && (
          <p role="alert" className="text-danger text-sm">
            {error}
          </p>
        )}
      </section>
    </div>
  );
}
