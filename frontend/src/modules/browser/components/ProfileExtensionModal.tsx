import { useEffect, useState } from 'react'
import { Button, Modal, toast } from '../../../shared/components'
import type { BrowserExtension, BrowserProfile } from '../types'
import {
  fetchBrowserExtensions,
  fetchBrowserProfileExtensionSettings,
  saveBrowserProfileExtensionSettings,
} from '../api/extensions'
import { ProfileExtensionPicker } from './ProfileExtensionPicker'

interface ProfileExtensionModalProps {
  open: boolean
  profile: BrowserProfile | null
  onClose: () => void
}

export function ProfileExtensionModal({ open, profile, onClose }: ProfileExtensionModalProps) {
  const [extensions, setExtensions] = useState<BrowserExtension[]>([])
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open || !profile) return
    setLoading(true)
    Promise.all([
      fetchBrowserExtensions(),
      fetchBrowserProfileExtensionSettings(profile.profileId),
    ]).then(([extensionItems, settings]) => {
      setExtensions(extensionItems)
      setSelectedIds(settings.extensionIds)
    }).catch((error: any) => {
      toast.error(error?.message || '加载实例插件配置失败')
    }).finally(() => setLoading(false))
  }, [open, profile])

  const handleSave = async () => {
    if (!profile) return
    setSaving(true)
    try {
      await saveBrowserProfileExtensionSettings(profile.profileId, selectedIds, true)
      toast.success('实例插件配置已保存')
      onClose()
    } catch (error: any) {
      toast.error(error?.message || '保存实例插件配置失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={profile ? `插件配置：${profile.profileName}` : '插件配置'}
      width="640px"
      footer={(
        <>
          <Button variant="secondary" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} loading={saving} disabled={loading}>保存</Button>
        </>
      )}
    >
      <div className="space-y-3">
        {loading ? (
          <div className="rounded-xl border border-[var(--color-border-default)] bg-[var(--color-bg-muted)] px-4 py-6 text-center text-sm text-[var(--color-text-muted)]">
            正在加载插件...
          </div>
        ) : (
          <ProfileExtensionPicker
            extensions={extensions}
            selectedIds={selectedIds}
            onChange={setSelectedIds}
          />
        )}
      </div>
    </Modal>
  )
}
