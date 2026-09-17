import * as Dialog from '@radix-ui/react-dialog';
import { Button } from './Button';
import { X } from 'lucide-react';
import { useT } from '@/i18n';

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  confirmText?: string;
  cancelText?: string;
  destructive?: boolean;
  onConfirm: () => void | Promise<void>;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText,
  cancelText,
  destructive,
  onConfirm,
}: ConfirmDialogProps) {
  const { t } = useT();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-bg/70 backdrop-blur-sm animate-fade-in" />
        <Dialog.Content
          className="fixed left-1/2 top-1/2 z-50 w-[420px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2
                     rounded-lg border border-border bg-bg-panel p-5 shadow-soft animate-fade-in"
        >
          <div className="relative pr-7">
            <Dialog.Title className="text-left text-base font-semibold text-fg">{title}</Dialog.Title>
            <Dialog.Close asChild>
              <button
                aria-label={t('action.close')}
                className="absolute right-0 top-0 grid h-6 w-6 place-items-center text-fg-subtle hover:text-fg transition-colors"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>
          {description && (
            <Dialog.Description className="mt-2 text-left text-sm text-fg-muted leading-relaxed">
              {description}
            </Dialog.Description>
          )}
          <div className="mt-5 flex items-center justify-end gap-2">
            <Dialog.Close asChild>
              <Button variant="ghost" size="sm">
                {cancelText ?? t('action.cancel')}
              </Button>
            </Dialog.Close>
            <Button
              variant={destructive ? 'danger' : 'primary'}
              size="sm"
              onClick={async () => {
                await onConfirm();
                onOpenChange(false);
              }}
            >
              {confirmText ?? t('action.confirm')}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
