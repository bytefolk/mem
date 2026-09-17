const tokenColor = (name) => `rgb(var(--${name}) / calc(var(--${name}-opacity, 1) * <alpha-value>))`;

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // Surface tokens — driven by CSS variables so themes swap at runtime.
        bg: {
          DEFAULT: tokenColor('bg'),
          subtle: tokenColor('bg-subtle'),
          panel: tokenColor('bg-panel'),
          inset: tokenColor('bg-inset'),
        },
        fg: {
          DEFAULT: tokenColor('fg'),
          muted: tokenColor('fg-muted'),
          subtle: tokenColor('fg-subtle'),
        },
        border: {
          DEFAULT: tokenColor('border'),
          strong: tokenColor('border-strong'),
        },
        accent: {
          DEFAULT: tokenColor('accent'),
          hover: tokenColor('accent-hover'),
          muted: tokenColor('accent-muted'),
          solid: tokenColor('accent-solid'),
          'solid-hover': tokenColor('accent-solid-hover'),
          foreground: tokenColor('accent-foreground'),
        },
        ai: tokenColor('ai'),
        success: tokenColor('success'),
        warn: tokenColor('warn'),
        danger: tokenColor('danger'),
      },
      fontFamily: { sans: ['var(--font-sans)'], mono: ['var(--font-mono)'] },
      fontSize: {
        '2xs': ['0.75rem', { lineHeight: '1rem' }],
      },
      boxShadow: {
        soft: 'var(--shadow-soft)',
        glow: '0 0 0 1px rgb(var(--accent) / 0.35), 0 8px 24px -8px rgb(var(--accent) / 0.45)',
      },
      backgroundImage: {
        'grid-fade':
          'linear-gradient(to bottom, rgb(var(--bg)) 0%, transparent 30%, transparent 70%, rgb(var(--bg)) 100%)',
        'dot-grid':
          'radial-gradient(circle at 1px 1px, rgb(var(--border-strong) / 0.5) 1px, transparent 0)',
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        shimmer: {
          '100%': { transform: 'translateX(100%)' },
        },
        'slide-up': {
          from: { opacity: '0', transform: 'translateY(12px) scale(0.98)' },
          to: { opacity: '1', transform: 'translateY(0) scale(1)' },
        },
        breathe: {
          '0%, 100%': { boxShadow: '0 0 0 0 rgb(var(--accent) / 0.45), 0 10px 30px -8px rgb(var(--accent) / 0.5)' },
          '50%': { boxShadow: '0 0 0 8px rgb(var(--accent) / 0), 0 10px 30px -8px rgb(var(--accent) / 0.5)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 200ms ease-out',
        shimmer: 'shimmer 1.6s infinite',
        'slide-up': 'slide-up 240ms cubic-bezier(0.22, 1, 0.36, 1)',
        breathe: 'breathe 3.5s ease-in-out infinite',
      },
    },
  },
  plugins: [],
};
