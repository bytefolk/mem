import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '@/i18n';
import { ConfirmDialog } from './ConfirmDialog';

describe('ConfirmDialog action labels', () => {
  it.each([
    ['en', 'Cancel', 'Confirm'],
    ['zh', '取消', '确认'],
  ])('keeps default actions visible and cancel safe in %s', async (language, cancel, confirm) => {
    const onConfirm = vi.fn();
    const onOpenChange = vi.fn();
    const dialogTitle = 'Fixture action';
    const dialogDescription = 'Synthetic confirmation fixture';
    render(
      <I18nProvider>
        <ConfirmDialog
          open
          title={dialogTitle}
          description={dialogDescription}
          onOpenChange={onOpenChange}
          onConfirm={onConfirm}
        />
      </I18nProvider>,
    );

    act(() => window.dispatchEvent(new StorageEvent('storage', {
      key: 'mem.lang',
      newValue: language,
    })));

    expect(screen.getByRole('button', { name: confirm })).toBeVisible();
    await userEvent.click(screen.getByRole('button', { name: cancel }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
