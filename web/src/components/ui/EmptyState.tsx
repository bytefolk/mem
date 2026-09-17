import * as React from 'react';
import { cn } from '@/lib/cn';

export interface EmptyStateProps {
  icon?: React.ReactNode;
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
  compact?: boolean;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
  compact = false,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        'ui-empty-state flex flex-col items-center justify-center text-center',
        compact ? 'ui-empty-state--compact py-6 px-4' : 'py-12 px-6',
        className,
      )}
    >
      {icon && (
        <div
          aria-hidden="true"
          className="ui-empty-state__icon mb-3 text-fg-muted [&>svg]:h-8 [&>svg]:w-8"
        >
          {icon}
        </div>
      )}
      <h3 className="ui-empty-state__title text-sm font-semibold text-fg leading-normal">
        {title}
      </h3>
      {description && (
        <div className="ui-empty-state__description mt-2 max-w-md text-sm text-fg-muted leading-normal">
          {description}
        </div>
      )}
      {action && <div className="ui-empty-state__action mt-4">{action}</div>}
    </div>
  );
}
