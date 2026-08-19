import { useEffect, useMemo, useState } from 'react'
import { ExternalLink, Search } from 'lucide-react'
import { Button, Modal, toast } from '../../../shared/components'
import type { BrowserExtension, BrowserProfile, BrowserProfileExtensionSettings } from '../types'
import { fetchBrowserProfileExtensionSettings, saveBrowserProfileExtensionSettings } from '../api/extensions'
import { fetchBrowserProfiles } from '../api/profiles'
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { extensionHistoryActionLabel, formatExtensionTime, sameStringSet, type ExtensionHistoryRecord } from './extensionManagementUtils'

const UNTAGGED_PROFILE_SECTION = '未打标签'

export interface ExtensionProfileLimitModalProps {
  open: boolean
  extension: BrowserExtension | null
  onClose: () => void
}

export function ExtensionProfileLimitModal({ open, extension, onClose }: ExtensionProfileLimitModalProps) {
  const [profiles, setProfiles] = useState<BrowserProfile[]>([])
  const [settingsByProfile, setSettingsByProfile] = useState<Record<string, BrowserProfileExtensionSettings>>({})
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const selectedSet = useMemo(() => new Set(selectedIds), [selectedIds])
  const profileSections = useMemo(() => {
    const buckets = new Map<string, BrowserProfile[]>()
    profiles.forEach((profile) => {
      const primaryTag = (profile.tags || []).map((tag) => tag.trim()).find(Boolean) || UNTAGGED_PROFILE_SECTION
      if (!buckets.has(primaryTag)) buckets.set(primaryTag, [])
      buckets.get(primaryTag)!.push(profile)
    })

    return Array.from(buckets.entries())
      .sort(([a], [b]) => {
        if (a === UNTAGGED_PROFILE_SECTION) return 1
        if (b === UNTAGGED_PROFILE_SECTION) return -1
        return a.localeCompare(b, 'zh-CN')
      })
      .map(([tag, items]) => ({ tag, profiles: items }))
  }, [profiles])

  useEffect(() => {
    if (!open || !extension) return
    setLoading(true)
    fetchBrowserProfiles().then(async (profileItems) => {
      const profileSettings = await Promise.all(profileItems.map(async (profile) => ({
        profile,
        settings: await fetchBrowserProfileExtensionSettings(profile.profileId),
      })))
      const settingsMap: Record<string, BrowserProfileExtensionSettings> = {}
      profileSettings.forEach(({ profile, settings }) => {
        settingsMap[profile.profileId] = settings
      })
      setProfiles(profileItems)
      setSettingsByProfile(settingsMap)
      setSelectedIds(profileItems
        .filter((profile) => {
          const settings = settingsMap[profile.profileId]
          return settings?.extensionIds.includes(extension.extensionId)
        })
        .map((profile) => profile.profileId))
    }).catch((error: any) => {
      toast.error(error?.message || '加载适用实例失败')
    }).finally(() => setLoading(false))
  }, [open, extension])

  const toggleProfile = (profileId: string, checked: boolean) => {
    setSelectedIds((current) => {
      if (checked) return current.includes(profileId) ? current : [...current, profileId]
      return current.filter((item) => item !== profileId)
    })
  }

  const toggleSection = (profileIds: string[], checked: boolean) => {
    setSelectedIds((current) => {
      const next = new Set(current)
      profileIds.forEach((profileId) => {
        if (checked) next.add(profileId)
        else next.delete(profileId)
      })
      return Array.from(next)
    })
  }

  const handleSave = async () => {
    if (!extension) return
    setSaving(true)
    try {
      const selected = new Set(selectedIds)
      const saveTasks = profiles.map((profile) => {
        const current = settingsByProfile[profile.profileId]
        const baseIds = current?.extensionIds || []
        const nextIds = selected.has(profile.profileId)
          ? Array.from(new Set([...baseIds, extension.extensionId]))
          : baseIds.filter((extensionId) => extensionId !== extension.extensionId)

        if (sameStringSet(baseIds, nextIds)) return null
        return saveBrowserProfileExtensionSettings(profile.profileId, nextIds, true)
      }).filter((task): task is Promise<BrowserProfileExtensionSettings> => task !== null)

      if (saveTasks.length > 0) await Promise.all(saveTasks)
      toast.success('适用实例已保存')
      onClose()
    } catch (error: any) {
      toast.error(error?.message || '保存适用实例失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={extension ? `适用实例：${extension.name || extension.extensionId}` : '适用实例'}
      width="680px"
      footer={(
        <>
          <Button variant="secondary" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} loading={saving} disabled={loading || !extension}>保存</Button>
        </>
      )}
    >
      {loading ? (
        <div className="py-8 text-center text-sm text-[var(--color-text-muted)]">正在加载实例...</div>
      ) : (
        <div className="space-y-3">
          <div className="rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-muted)] px-3 py-2 text-sm text-[var(--color-text-secondary)]">
            勾选的实例会加载此插件；未勾选的实例会排除此插件。
          </div>
          <div className="max-h-[420px] space-y-3 overflow-auto pr-1">
            {profileSections.map((section) => {
              const sectionProfileIds = section.profiles.map((profile) => profile.profileId)
              const selectedCount = sectionProfileIds.filter((profileId) => selectedSet.has(profileId)).length
              const allSelected = sectionProfileIds.length > 0 && selectedCount === sectionProfileIds.length

              return (
                <section key={section.tag} className="rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-surface)]">
                  <div className="flex items-center justify-between gap-3 border-b border-[var(--color-border-muted)] px-3 py-2">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium text-[var(--color-text-primary)]">{section.tag}</div>
                      <div className="text-xs text-[var(--color-text-muted)]">已选 {selectedCount} / {sectionProfileIds.length}</div>
                    </div>
                    <Button size="sm" variant="ghost" onClick={() => toggleSection(sectionProfileIds, !allSelected)}>
                      {allSelected ? '取消此标签' : '选择此标签'}
                    </Button>
                  </div>
                  <div className="divide-y divide-[var(--color-border-muted)]">
                    {section.profiles.map((profile) => (
                      <label key={profile.profileId} className="flex items-start gap-3 px-3 py-2">
                        <input
                          type="checkbox"
                          checked={selectedSet.has(profile.profileId)}
                          onChange={(event) => toggleProfile(profile.profileId, event.target.checked)}
                          className="mt-1 h-4 w-4 shrink-0 rounded accent-[var(--color-accent)]"
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-2 text-sm font-medium text-[var(--color-text-primary)]">
                            <span>{profile.profileName || profile.profileId}</span>
                            {profile.running ? <span className="rounded bg-green-50 px-1.5 py-0.5 text-xs text-green-700">运行中</span> : null}
                          </div>
                          <div className="mt-1 break-all font-mono text-xs text-[var(--color-text-muted)]">{profile.profileId}</div>
                        </div>
                      </label>
                    ))}
                  </div>
                </section>
              )
            })}

            {profiles.length === 0 ? (
              <div className="rounded-xl border border-dashed border-[var(--color-border-default)] bg-[var(--color-bg-muted)] px-4 py-8 text-center text-sm text-[var(--color-text-muted)]">
                暂无实例
              </div>
            ) : null}
          </div>
        </div>
      )}
    </Modal>
  )
}

export interface ExtensionHistoryModalProps {
  open: boolean
  records: ExtensionHistoryRecord[]
  onClose: () => void
  onPick: (record: ExtensionHistoryRecord) => void
  onClear: () => void
}

export function ExtensionHistoryModal({ open, records, onClose, onPick, onClear }: ExtensionHistoryModalProps) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="插件历史"
      width="760px"
      footer={(
        <>
          <Button variant="secondary" onClick={onClear} disabled={records.length === 0}>清空历史</Button>
          <Button onClick={onClose}>关闭</Button>
        </>
      )}
    >
      <div className="max-h-[520px] overflow-y-auto">
        {records.length === 0 ? (
          <div className="rounded-xl border border-dashed border-[var(--color-border-default)] bg-[var(--color-bg-muted)] px-4 py-8 text-center text-sm text-[var(--color-text-muted)]">
            暂无历史记录
          </div>
        ) : (
          <div className="space-y-2">
            {records.map((record) => (
              <div key={record.id} className="rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] p-3">
                <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="rounded-full bg-[var(--color-bg-muted)] px-2 py-0.5 text-xs text-[var(--color-text-muted)]">{extensionHistoryActionLabel(record.action)}</span>
                      <span className={record.ok ? 'text-xs text-green-600' : 'text-xs text-red-500'}>{record.ok ? '成功' : '失败'}</span>
                      <span className="text-xs text-[var(--color-text-muted)]">{formatExtensionTime(record.createdAt)}</span>
                    </div>
                    <div className="mt-1 truncate text-sm font-medium text-[var(--color-text-primary)]">{record.name || record.extensionId || record.query}</div>
                    {record.extensionId ? <div className="mt-1 break-all font-mono text-xs text-[var(--color-text-muted)]">{record.extensionId}</div> : null}
                    {record.proxyLabel ? <div className="mt-1 text-xs text-[var(--color-text-muted)]">{record.proxyLabel}</div> : null}
                    {record.message ? <div className="mt-1 line-clamp-2 text-xs text-[var(--color-text-muted)]">{record.message}</div> : null}
                  </div>
                  <div className="flex shrink-0 gap-2">
                    <Button type="button" size="sm" variant="secondary" onClick={() => onPick(record)}>
                      <Search className="h-4 w-4" />
                      使用
                    </Button>
                    {record.storeUrl ? (
                      <Button type="button" size="sm" variant="secondary" onClick={() => BrowserOpenURL(record.storeUrl)}>
                        <ExternalLink className="h-4 w-4" />
                        商店
                      </Button>
                    ) : null}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </Modal>
  )
}
