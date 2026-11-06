import { type ReactNode } from 'react';
import { type LucideIcon } from 'lucide-react';

// ---- gradient palette ----
export type GradColor = 'gray' | 'lime' | 'teal' | 'green' | 'red' | 'amber' | 'pink' | 'purple';

const GRADIENTS: Record<GradColor, string> = {
  gray:   'bg-gradient-to-tr from-gray-700 to-gray-600 shadow-gray-900/30',
  lime:   'bg-gradient-to-tr from-lime-600 to-lime-400 shadow-lime-500/25',
  teal:   'bg-gradient-to-tr from-teal-600 to-teal-400 shadow-teal-500/20',
  green:  'bg-gradient-to-tr from-green-600 to-green-400 shadow-green-500/20',
  red:    'bg-gradient-to-tr from-red-600 to-red-400 shadow-red-500/20',
  amber:  'bg-gradient-to-tr from-amber-600 to-amber-400 shadow-amber-500/20',
  pink:   'bg-gradient-to-tr from-pink-600 to-pink-400 shadow-pink-500/20',
  purple: 'bg-gradient-to-tr from-purple-600 to-purple-400 shadow-purple-500/20',
};

// ---- Floating-icon stat card (Material Tailwind pattern) ----
export function StatCard({
  color = 'gray',
  icon: Icon,
  title,
  value,
  footer,
}: {
  color?: GradColor;
  icon: LucideIcon;
  title: string;
  value: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="relative mt-6">
      {/* card body first so icon (rendered after) stacks above it */}
      <div className="rounded-xl border border-white/5 bg-gray-900 shadow-sm">
        <div className="p-4 text-right">
          <p className="text-[11px] font-semibold uppercase tracking-widest text-slate-500 dark:text-gray-400">
            {title}
          </p>
          <div className="mt-1 text-2xl font-bold tabular-nums text-slate-700 dark:text-white">
            {value}
          </div>
        </div>
        {footer !== undefined && (
          <div className="border-t border-slate-50 dark:border-gray-700/50 px-4 py-3">
            {footer}
          </div>
        )}
      </div>
      {/* floating gradient icon — rendered after card so it paints on top */}
      <div className={`absolute left-4 -top-6 h-12 w-12 rounded-xl grid place-items-center shadow-lg ${GRADIENTS[color]}`}>
        <Icon className="h-6 w-6 text-white" strokeWidth={2} />
      </div>
    </div>
  );
}

// ---- Gradient-header card for tables ----
export function GradCard({
  title,
  subtitle,
  action,
  children,
}: {
  title: string;
  subtitle?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="rounded-xl overflow-hidden border border-slate-100 dark:border-gray-700 shadow-sm">
      <div className="bg-gradient-to-tr from-gray-900 to-gray-800 px-6 py-5 flex items-start justify-between gap-4">
        <div>
          <h6 className="text-sm font-semibold text-white">{title}</h6>
          {subtitle && <p className="mt-0.5 text-[11px] text-white/50">{subtitle}</p>}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
      <div className="bg-white dark:bg-gray-800 overflow-x-auto px-0 pt-0 pb-2">
        {children}
      </div>
    </div>
  );
}

// ---- Simple sectioned card ----
export function Card({
  title,
  subtitle,
  children,
  className = '',
}: {
  title?: string;
  subtitle?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={`rounded-xl border border-white/5 bg-gray-900 shadow-sm ${className}`}>
      {title && (
        <div className="px-6 py-4 border-b border-slate-50 dark:border-gray-700">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-white">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-slate-500 dark:text-gray-400">{subtitle}</p>}
        </div>
      )}
      {children}
    </div>
  );
}

// ---- Chip / badge ----
export type ChipColor = 'green' | 'red' | 'amber' | 'lime' | 'gray' | 'purple' | 'teal';

const CHIP_CLS: Record<ChipColor, string> = {
  green:  'bg-gradient-to-tr from-green-600 to-green-500 text-white',
  red:    'bg-gradient-to-tr from-red-600 to-red-400 text-white',
  amber:  'bg-gradient-to-tr from-amber-500 to-amber-400 text-white',
  lime:   'bg-gradient-to-tr from-lime-500 to-lime-400 text-gray-900',
  gray:   'bg-gray-700 text-gray-300',
  purple: 'bg-gradient-to-tr from-purple-600 to-purple-400 text-white',
  teal:   'bg-gradient-to-tr from-teal-600 to-teal-400 text-white',
};

export function Chip({
  color = 'gray',
  children,
  className = '',
}: {
  color?: ChipColor;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-md py-0.5 px-2 text-[11px] font-medium w-fit ${CHIP_CLS[color]} ${className}`}>
      {children}
    </span>
  );
}
