import { Puzzle } from 'lucide-react'
import type { BrowserExtension } from '../types'

interface ProfileExtensionPickerProps {
  extensions: BrowserExtension[]
  selectedIds: string[]
  disabled?: boolean
  emptyText?: string
  onChange: (extensionIds: string[]) => void
}

export function ProfileExtensionPicker({
  extensions,
  selectedIds,
  disabled = false,
  emptyText = '还没有安装插件，请先到插件包管理页面导入。',
  onChange,
}: ProfileExtensionPickerProps) {
  const selectedSet = new Set(selectedIds)

  const toggleExtension = (extensionId: string, checked: boolean) => {
    if (checked) {
      onChange(selectedIds.includes(extensionId) ? selectedIds : [...selectedIds, extensionId])
      return
    }
    onChange(selectedIds.filter((item) => item !== extensionId))
  }

  if (extensions.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-[var(--color-border-default)] bg-[var(--color-bg-muted)] px-4 py-6 text-center text-sm text-[var(--color-text-muted)]">
        {emptyText}
      </div>
    )
  }

  return (
    <div className="max-h-[360px] space-y-2 overflow-auto pr-1">
      {extensions.map((extension) => {
        const displayName = extension.name || extension.extensionId
        return (
          <label
            key={extension.extensionId}
            className="flex items-center gap-3 rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-3 py-2"
          >
            <input
              type="checkbox"
              disabled={disabled}
              checked={selectedSet.has(extension.extensionId)}
              onChange={(event) => toggleExtension(extension.extensionId, event.target.checked)}
              className="h-4 w-4 shrink-0 rounded accent-[var(--color-accent)]"
            />
            <div className="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-muted)]">
              {extension.iconDataUrl ? (
                <img src={extension.iconDataUrl} alt="" className="h-7 w-7 object-contain" />
              ) : (
                <Puzzle className="h-5 w-5 text-[var(--color-text-muted)]" />
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-[var(--color-text-primary)]">
                <span className="truncate">{displayName}</span>
                {extension.version ? <span className="text-xs font-normal text-[var(--color-text-muted)]">v{extension.version}</span> : null}
              </div>
              <div className="mt-0.5 break-all font-mono text-xs text-[var(--color-text-muted)]">{extension.extensionId}</div>
            </div>
          </label>
        )
      })}
    </div>
  )
}
