import { Monitor, Moon, Sun } from 'lucide-react'
import { useTheme, type Theme } from '../theme'
import { cn } from './ui'

const OPTIONS: { id: Theme; label: string; icon: typeof Sun }[] = [
  { id: 'light', label: 'Light', icon: Sun },
  { id: 'dark', label: 'Dark', icon: Moon },
  { id: 'system', label: 'Match system', icon: Monitor },
]

export default function ThemeToggle({ className }: { className?: string }) {
  const { theme, setTheme } = useTheme()
  return (
    <div
      role="group"
      aria-label="Theme"
      className={cn('inline-flex items-center gap-0.5 rounded-lg border border-zinc-800 bg-zinc-900/50 p-0.5', className)}
    >
      {OPTIONS.map(({ id, label, icon: Icon }) => (
        <button
          key={id}
          type="button"
          title={label}
          aria-label={label}
          aria-pressed={theme === id}
          onClick={() => setTheme(id)}
          className={cn(
            'rounded-md p-1.5 transition-colors',
            theme === id ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-500 hover:text-zinc-200',
          )}
        >
          <Icon size={13} />
        </button>
      ))}
    </div>
  )
}
