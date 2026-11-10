import { type ReactNode } from 'react';
import { type LucideIcon } from 'lucide-react';

export type GradColor = 'gray' | 'lime' | 'teal' | 'green' | 'red' | 'amber' | 'pink' | 'purple';

const ACCENT_BAR: Record<GradColor, string> = {
  lime:   'bg-lime-400',
  teal:   'bg-lime-400',
  green:  'bg-green-400',
  red:    'bg-red-400',
  amber:  'bg-amber-400',
  gray:   'bg-white/20',
  pink:   'bg-lime-400',
  purple: 'bg-white/20',
};

const ICON_CLS: Record<GradColor, string> = {
  lime:   'text-lime-400',
  teal:   'text-lime-400',
  green:  'text-green-400',
  red:    'text-red-400',
  amber:  'text-amber-400',
  gray:   'text-gray-500',
  pink:   'text-lime-400',
  purple: 'text-gray-500',
};

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
    <div className="relative rounded-xl bg-[#111] border border-white/8 overflow-hidden flex items-center gap-4 pl-5 pr-5 py-4">
      <span className={`absolute left-0 top-0 h-full w-[3px] ${ACCENT_BAR[color]}`} />
      <div className="shrink-0 size-9 rounded-lg bg-white/5 grid place-items-center">
        <Icon size={15} className={ICON_CLS[color]} strokeWidth={1.75} />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-[10px] font-semibold uppercase tracking-widest text-gray-500">{title}</p>
        <div className="mt-0.5 text-xl font-bold tabular-nums text-white truncate">{value}</div>
      </div>
      {footer !== undefined && (
        <div className="shrink-0">{footer}</div>
      )}
    </div>
  );
}

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
    <div className="rounded-xl overflow-hidden border border-white/8">
      <div className="bg-[#161616] px-6 py-4 flex items-center justify-between gap-4 border-b border-white/8">
        <div>
          <h6 className="text-sm font-semibold text-white">{title}</h6>
          {subtitle && <p className="mt-0.5 text-[11px] text-gray-500">{subtitle}</p>}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
      <div className="bg-[#111] overflow-x-auto">
        {children}
      </div>
    </div>
  );
}

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
    <div className={`rounded-xl border border-white/8 bg-[#111] ${className}`}>
      {title && (
        <div className="px-6 py-4 border-b border-white/8">
          <h2 className="text-sm font-semibold text-white">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-gray-500">{subtitle}</p>}
        </div>
      )}
      {children}
    </div>
  );
}

export type ChipColor = 'green' | 'red' | 'amber' | 'lime' | 'gray' | 'purple' | 'teal';

const CHIP_CLS: Record<ChipColor, string> = {
  green:  'border border-green-500/30 text-green-400 bg-green-500/10',
  red:    'border border-red-500/30 text-red-400 bg-red-500/10',
  amber:  'border border-amber-400/30 text-amber-400 bg-amber-400/10',
  lime:   'border border-lime-400/30 text-lime-400 bg-lime-400/10',
  gray:   'border border-white/10 text-gray-400 bg-white/5',
  purple: 'border border-white/10 text-gray-400 bg-white/5',
  teal:   'border border-lime-400/30 text-lime-400 bg-lime-400/10',
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
