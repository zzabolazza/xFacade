import { useEffect, useState } from 'react'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import { toast } from '../../../shared/components'
import { flushThemeForBackup } from '../../../shared/theme/theme'
import type { BrowserInputSyncStatus, BrowserProfile, BrowserProfileCopyOptions, BrowserProxy } from '../types'
import { BrowserListHeader } from '../components/BrowserListLayout'
import { BatchToolbar } from '../components/BrowserListWidgets'
import { BrowserProfilesPanel } from '../components/BrowserProfilesPanel'
import { BrowserBackupModal } from '../components/BrowserBackupModal'
import { ProxyPickerModal } from '../components/ProxyPickerModal'
import { ProfileExtensionModal } from '../components/ProfileExtensionModal'
import { createBrowserProfileCopyOptions, isBrowserProfileCopyOptionsValid } from '../copyOptions'
import { buildBrowserProfileCopyName } from '../copyName'
import { resolveActionFeedback } from '../utils/actionErrors'
import { buildBrowserWindowTiles } from '../utils/windowTiling'
import { BrowserListDialogs } from './browserList/BrowserListDialogs'
import { useBrowserListDerived, useBrowserListViewState } from './browserList/useBrowserListViewState'
import { useBrowserListData } from './browserList/useBrowserListData'
import { useBrowserProfileActions } from './browserList/useBrowserProfileActions'
import {
  copyBrowserProfile,
  deleteBrowserProfile,
  exportBrowserProfilePackage,
  importBrowserProfilePackage,
  setBrowserInstancesWindowBounds,
  setBrowserInstanceWindowBounds,
  startBrowserInstanceWithParams,
  stopBrowserInstance,
  updateBrowserProfile,
  exportFullBrowserBackup,
  importFullBrowserBackup,
  pickFullBrowserBackupFile,
  fetchBrowserInputSyncStatus,
  runBrowserInputSyncSpecialMode,
  startBrowserInputSync,
  stopBrowserInputSync,
} from '../api'

type BackupLoadingMode = 'none' | 'export' | 'import-merge' | 'import-reset'

const directProxyID = '__direct__'

export function BrowserListPage() {
  const {
    viewMode,
    setViewMode,
    filters,
    setFilters,
  } = useBrowserListViewState()

  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [batchOperation, setBatchOperation] = useState<'start' | 'stop' | 'delete' | null>(null)
  const batchLoading = batchOperation !== null
  const batchStarting = batchOperation === 'start'
  const batchStopping = batchOperation === 'stop'
  const [retileLoading, setRetileLoading] = useState(false)
  const [inputSyncStatus, setInputSyncStatus] = useState<BrowserInputSyncStatus | null>(null)
  const [inputSyncLoading, setInputSyncLoading] = useState(false)
  const [profilePackageBusy, setProfilePackageBusy] = useState(false)
  const [backupModalOpen, setBackupModalOpen] = useState(false)
  const [backupLoadingMode, setBackupLoadingMode] = useState<BackupLoadingMode>('none')
  const [deleteConfirm, setDeleteConfirm] = useState<{
    open: boolean
    mode: 'single' | 'batch'
    profileId?: string
    profileName?: string
    count: number
  }>({ open: false, mode: 'single', count: 0 })

  // 代理不支持弹窗
  const [proxyErrorModal, setProxyErrorModal] = useState(false)
  const [proxyErrorMsg, setProxyErrorMsg] = useState('')
  const [opError, setOpError] = useState('')
  const [pendingStartId, setPendingStartId] = useState<string | null>(null)

  const [extensionModal, setExtensionModal] = useState<{ open: boolean; profile: BrowserProfile | null }>({ open: false, profile: null })
  const openExtensionModal = (profile: BrowserProfile) => setExtensionModal({ open: true, profile })
  const closeExtensionModal = () => setExtensionModal({ open: false, profile: null })

  const [proxyPickerProfile, setProxyPickerProfile] = useState<BrowserProfile | null>(null)

  // 复制弹窗
  const [copyModal, setCopyModal] = useState<{ open: boolean; profile: BrowserProfile | null }>({ open: false, profile: null })
  const [copyName, setCopyName] = useState('')
  const [copyOptions, setCopyOptions] = useState<BrowserProfileCopyOptions>(() => createBrowserProfileCopyOptions())
  const [copying, setCopying] = useState(false)

  const openCopyModal = (profile: BrowserProfile) => {
    setCopyName(buildBrowserProfileCopyName(profile.profileName))
    setCopyOptions(createBrowserProfileCopyOptions())
    setCopyModal({ open: true, profile })
  }
  const closeCopyModal = () => {
    setCopyModal({ open: false, profile: null })
    setCopyName('')
    setCopyOptions(createBrowserProfileCopyOptions())
  }
  const {
    profiles,
    loading,
    proxies,
    cores,
    startingIds,
    stoppingIds,
    setStartingIds,
    setStoppingIds,
    updatePendingIds,
    mergeProfileState,
    updateProxiesState,
    loadProfiles,
  } = useBrowserListData()
  const {
    runningCount,
    allTags,
    filteredProfiles,
    resolveProfileCore,
    getProfileCoreLabel,
    isProfileStarting,
    isProfileStopping,
    isProfileBusy,
    getProfileStatus,
  } = useBrowserListDerived(profiles, cores, filters, startingIds, stoppingIds)
  const {
    handleStart,
    handleStartDirect,
    handleStop,
    handleRestart,
    handleFocus,
  } = useBrowserProfileActions({
    profiles,
    setProxyErrorModal,
    setProxyErrorMsg,
    setPendingStartId,
    setOpError,
    setStartingIds,
    setStoppingIds,
    updatePendingIds,
    mergeProfileState,
    loadProfiles,
  })

  useEffect(() => {
    let cancelled = false
    fetchBrowserInputSyncStatus()
      .then((status) => {
        if (!cancelled) setInputSyncStatus(status)
      })
      .catch(() => {
        if (!cancelled) setInputSyncStatus(null)
      })
    const off = EventsOn('browser:input-sync:status', (status: BrowserInputSyncStatus) => {
      setInputSyncStatus(status)
    })
    return () => {
      cancelled = true
      off()
    }
  }, [])
  // 批量操作
  const toggleSelect = (profileId: string) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      next.has(profileId) ? next.delete(profileId) : next.add(profileId)
      return next
    })
  }



  const handleSelectAll = () => {
    setSelectedIds(new Set(filteredProfiles.map(p => p.profileId)))
  }

  const handleDeselectAll = () => {
    setSelectedIds(new Set())
  }

  const handleBatchStart = async () => {
    const startTargets = Array.from(selectedIds)
      .map(id => profiles.find(profile => profile.profileId === id))
      .filter((profile): profile is BrowserProfile => !!profile && !profile.running)
    if (startTargets.length === 0) {
      toast.warning('没有需要启动的实例')
      return
    }

    const tiles = buildBrowserWindowTiles(startTargets.length)
    setBatchOperation('start')
    let success = 0, pending = 0, failed = 0
    const pendingMessages: string[] = []
    const failureMessages: string[] = []
    for (let index = 0; index < startTargets.length; index += 1) {
      const profile = startTargets[index]
      const tile = tiles[index]
      updatePendingIds(setStartingIds, profile.profileId, true)
      try {
        const startedProfile = await startBrowserInstanceWithParams(profile.profileId, {
          extraLaunchArgs: tile?.launchArgs || [],
        })
        mergeProfileState(startedProfile)
        success++
      } catch (error: any) {
        const feedback = resolveActionFeedback(error, '实例启动失败')
        if (feedback.pendingAttach) {
          pending++
          pendingMessages.push(`${profile.profileName}：${feedback.message}`)
        } else {
          failed++
          failureMessages.push(`${profile.profileName}：${feedback.message}`)
        }
      } finally {
        updatePendingIds(setStartingIds, profile.profileId, false)
      }
    }
    setBatchOperation(null)
    const summary = [`成功 ${success}`]
    if (pending > 0) summary.push(`待接管 ${pending}`)
    if (failed > 0) summary.push(`失败 ${failed}`)
    toast.success(`批量启动完成：${summary.join('，')}`)
    if (pendingMessages.length > 0) {
      const preview = pendingMessages.slice(0, 3)
      const more = pendingMessages.length > preview.length ? `\n另有 ${pendingMessages.length - preview.length} 个实例已打开窗口，仍在后台接管。` : ''
      toast.warning(`以下实例已打开窗口，仍在后台接管：\n${preview.join('\n')}${more}`)
    }
    if (failureMessages.length > 0) {
      const preview = failureMessages.slice(0, 3)
      const more = failureMessages.length > preview.length ? `\n另有 ${failureMessages.length - preview.length} 个实例启动失败，请逐个检查。` : ''
      toast.error(`以下实例启动失败：\n${preview.join('\n')}${more}`)
    }
    loadProfiles()
  }

  const handleRetileRunning = async () => {
    const runningTargets = Array.from(selectedIds)
      .map(id => profiles.find(profile => profile.profileId === id))
      .filter((profile): profile is BrowserProfile => !!profile && profile.running)
    if (runningTargets.length === 0) {
      toast.warning('没有已选的运行实例')
      return
    }

    const tiles = buildBrowserWindowTiles(runningTargets.length)
    setRetileLoading(true)
    let success = 0, failed = 0
    const failureMessages: string[] = []
    const tileRequests = runningTargets.map((profile, index) => {
      const tile = tiles[index]
      return {
        profileId: profile.profileId,
        bounds: {
          left: tile?.x || 0,
          top: tile?.y || 0,
          width: tile?.width || 1,
          height: tile?.height || 1,
        },
      }
    })

    try {
      const batchResults = await setBrowserInstancesWindowBounds(tileRequests, true)
      if (batchResults) {
        batchResults.forEach((result) => {
          if (result.ready && !result.warning) {
            success++
          } else {
            failed++
            failureMessages.push(`${result.profileName || result.profileId}：${result.warning || '平铺后未能读取实际窗口/视口'}`)
          }
        })
        setRetileLoading(false)
        if (success > 0) {
          toast.success(`平铺完成：成功 ${success}${failed > 0 ? `，失败 ${failed}` : ''}`)
        } else {
          toast.error(`平铺失败：失败 ${failed}`)
        }
        if (failureMessages.length > 0) {
          const preview = failureMessages.slice(0, 3)
          const more = failureMessages.length > preview.length ? `\n另有 ${failureMessages.length - preview.length} 个实例平铺失败。` : ''
          toast.error(`以下实例平铺失败：\n${preview.join('\n')}${more}`)
        }
        await loadProfiles({ silent: true, syncRuntimeState: true })
        return
      }
    } catch (error: any) {
      const feedback = resolveActionFeedback(error, '平铺失败')
      failureMessages.push(feedback.message)
    }

    for (let index = 0; index < runningTargets.length; index += 1) {
      const profile = runningTargets[index]
      const request = tileRequests[index]
      if (!request) continue
      try {
        const updatedProfile = await setBrowserInstanceWindowBounds(profile.profileId, request.bounds)
        mergeProfileState(updatedProfile)
        success++
      } catch (error: any) {
        failed++
        const feedback = resolveActionFeedback(error, '平铺失败')
        failureMessages.push(`${profile.profileName}：${feedback.message}`)
      }
    }
    setRetileLoading(false)
    if (success > 0) {
      toast.success(`平铺完成：成功 ${success}${failed > 0 ? `，失败 ${failed}` : ''}`)
    } else {
      toast.error(`平铺失败：失败 ${failed}`)
    }
    if (failureMessages.length > 0) {
      const preview = failureMessages.slice(0, 3)
      const more = failureMessages.length > preview.length ? `\n另有 ${failureMessages.length - preview.length} 个实例平铺失败。` : ''
      toast.error(`以下实例平铺失败：\n${preview.join('\n')}${more}`)
    }
    await loadProfiles({ silent: true, syncRuntimeState: true })
  }

  const handleStartInputSync = async () => {
    const runningTargets = profiles.filter(profile => selectedIds.has(profile.profileId) && profile.running)
    if (runningTargets.length < 2) {
      toast.warning('请至少选择 2 个运行中的实例')
      return
    }
    const [master, ...targets] = runningTargets
    setInputSyncLoading(true)
    try {
      const status = await startBrowserInputSync({
        masterProfileId: master.profileId,
        targetProfileIds: targets.map(profile => profile.profileId),
        mode: 'page',
      })
      setInputSyncStatus(status)
      const readyCount = status.targets.filter(target => target.ready).length
      toast.success(`输入同步已开启：主控 ${master.profileName}，目标 ${readyCount} 个`)
      const warnings = status.targets.filter(target => target.warning)
      if (warnings.length > 0) {
        const preview = warnings.slice(0, 3).map(target => `${target.profileName || target.profileId}：${target.warning}`)
        const more = warnings.length > preview.length ? `\n另有 ${warnings.length - preview.length} 个目标不可用。` : ''
        toast.warning(`部分目标未加入同步：\n${preview.join('\n')}${more}`)
      }
    } catch (error: any) {
      toast.error(error?.message || '输入同步启动失败')
    } finally {
      setInputSyncLoading(false)
    }
  }

  const handleStopInputSync = async () => {
    setInputSyncLoading(true)
    try {
      const status = await stopBrowserInputSync()
      setInputSyncStatus(status)
      toast.success('输入同步已停止')
    } catch (error: any) {
      toast.error(error?.message || '输入同步停止失败')
    } finally {
      setInputSyncLoading(false)
    }
  }

  const handleRunSpecialInputSync = async () => {
    const confirmed = window.confirm('窗口级同步会短暂切换窗口或移动鼠标，完成后再恢复主控窗口。是否继续？')
    if (!confirmed) return
    setInputSyncLoading(true)
    try {
      const status = await runBrowserInputSyncSpecialMode()
      setInputSyncStatus(status)
      toast.success('窗口级同步已完成')
    } catch (error: any) {
      toast.warning(error?.message || '窗口级同步暂不可用')
    } finally {
      setInputSyncLoading(false)
    }
  }

  const handleBatchStop = async () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    setBatchOperation('stop')
    let success = 0, failed = 0
    for (const id of ids) {
      const profile = profiles.find(p => p.profileId === id)
      if (!profile || !profile.running) continue
      updatePendingIds(setStoppingIds, id, true)
      try {
        const stoppedProfile = await stopBrowserInstance(id)
        mergeProfileState(stoppedProfile)
        success++
      } catch {
        failed++
      } finally {
        updatePendingIds(setStoppingIds, id, false)
      }
    }
    setBatchOperation(null)
    toast.success(`批量停止完成：成功 ${success}${failed > 0 ? `，失败 ${failed}` : ''}`)
    loadProfiles()
  }

  const handleBatchExport = async () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0 || profilePackageBusy) return
    const runningNames = profiles
      .filter(profile => ids.includes(profile.profileId) && profile.running)
      .map(profile => profile.profileName)
    if (runningNames.length > 0) {
      toast.error(`请先停止实例再导出：${runningNames.slice(0, 3).join('、')}${runningNames.length > 3 ? ' 等' : ''}`)
      return
    }
    setProfilePackageBusy(true)
    try {
      const result = await exportBrowserProfilePackage(ids)
      if (result.cancelled) return
      toast.success(`已导出 ${result.profileCount} 个实例`)
    } catch (error: any) {
      toast.error(error?.message || '导出实例失败')
    } finally {
      setProfilePackageBusy(false)
    }
  }

  const handleExportProfile = async (profile: BrowserProfile) => {
    if (profilePackageBusy) return
    if (profile.running) {
      toast.error(`请先停止实例再导出：${profile.profileName}`)
      return
    }
    setProfilePackageBusy(true)
    try {
      const result = await exportBrowserProfilePackage([profile.profileId])
      if (result.cancelled) return
      toast.success(`已导出：${profile.profileName}`)
    } catch (error: any) {
      toast.error(error?.message || '导出实例失败')
    } finally {
      setProfilePackageBusy(false)
    }
  }

  const handleImportProfiles = async () => {
    if (profilePackageBusy) return
    setProfilePackageBusy(true)
    try {
      const result = await importBrowserProfilePackage()
      if (result.cancelled) return
      const warnings = result.warnings || []
      if (warnings.length > 0) {
        toast.warning(`已导入 ${result.importedCount} 个实例，${warnings.length} 条提示：${warnings[0]}`)
      } else {
        toast.success(`已导入 ${result.importedCount} 个实例`)
      }
      setSelectedIds(new Set())
      await loadProfiles()
    } catch (error: any) {
      toast.error(error?.message || '导入实例失败')
    } finally {
      setProfilePackageBusy(false)
    }
  }

  const handleExportFullBackup = async (password: string) => {
    if (backupLoadingMode !== 'none') return
    if (runningCount > 0) {
      toast.warning(`建议先停止 ${runningCount} 个运行中实例后再备份`)
    }
    setBackupLoadingMode('export')
    try {
      await flushThemeForBackup()
      const result = await exportFullBrowserBackup(password)
      if (result.cancelled) return
      toast.success(result.zipPath ? `备份已导出：${result.zipPath}` : (result.message || '备份已导出'))
    } catch (error: unknown) {
      toast.error(typeof error === 'string' ? error : (error as any)?.message || '全量备份失败')
    } finally {
      setBackupLoadingMode('none')
    }
  }

  const handleImportFullBackup = async (resetFirst: boolean, password: string) => {
    if (backupLoadingMode !== 'none') return
    const mode: BackupLoadingMode = resetFirst ? 'import-reset' : 'import-merge'
    try {
      const picked = await pickFullBrowserBackupFile()
      if (picked.cancelled || !picked.path) {
        toast.info('已取消选择备份文件')
        return
      }
      setBackupLoadingMode(mode)
      const result = await importFullBrowserBackup(resetFirst, password, picked.path)
      if (result.cancelled) return
      toast.success(result.message || (resetFirst ? '备份已恢复' : '备份已合并'))
      setSelectedIds(new Set())
      setBackupModalOpen(false)
      await loadProfiles()
    } catch (error: unknown) {
      toast.error(typeof error === 'string' ? error : (error as any)?.message || '导入备份失败')
    } finally {
      setBackupLoadingMode('none')
    }
  }

  const openDeleteConfirm = (profileId: string) => {
    const profile = profiles.find(item => item.profileId === profileId)
    setDeleteConfirm({
      open: true,
      mode: 'single',
      profileId,
      profileName: profile?.profileName,
      count: 1,
    })
  }

  const openBatchDeleteConfirm = () => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    setDeleteConfirm({ open: true, mode: 'batch', count: ids.length })
  }

  const closeDeleteConfirm = () => {
    if (batchLoading) return
    setDeleteConfirm({ open: false, mode: 'single', count: 0 })
  }

  const handleConfirmDelete = async () => {
    const ids = deleteConfirm.mode === 'batch'
      ? Array.from(selectedIds)
      : deleteConfirm.profileId ? [deleteConfirm.profileId] : []
    if (ids.length === 0) return
    setBatchOperation('delete')
    try {
      for (const id of ids) {
        await deleteBrowserProfile(id)
      }
      setSelectedIds(prev => {
        const next = new Set(prev)
        ids.forEach(id => next.delete(id))
        return next
      })
      toast.success(ids.length > 1 ? `已删除 ${ids.length} 个实例` : '配置已删除')
      setDeleteConfirm({ open: false, mode: 'single', count: 0 })
      loadProfiles()
    } catch (error: any) {
      toast.error(error?.message || '删除失败')
    } finally {
      setBatchOperation(null)
    }
  }

  const handleCopy = async (profileId: string) => {
    if (!copyModal.profile) return
    setCopying(true)
    try {
      await copyBrowserProfile(profileId, copyName.trim(), copyOptions)
      toast.success('实例已复制')
      closeCopyModal()
      loadProfiles()
    } catch (error: any) {
      setOpError(typeof error === 'string' ? error : error?.message || '复制失败')
    } finally {
      setCopying(false)
    }
  }

  const copyConfirmDisabled =
    !copyName.trim() || !isBrowserProfileCopyOptionsValid(copyOptions)

  const saveProfileProxy = async (profile: BrowserProfile, proxy: BrowserProxy) => {
    try {
      const updated = await updateBrowserProfile(profile.profileId, {
        profileName: profile.profileName,
        userDataDir: profile.userDataDir,
        coreId: profile.coreId,
        fingerprintArgs: profile.fingerprintArgs,
        proxyId: proxy.proxyId,
        proxyConfig: '',
        launchArgs: profile.launchArgs,
        tags: profile.tags,
      })
      mergeProfileState(updated || { ...profile, proxyId: proxy.proxyId, proxyConfig: '' })
      toast.success('代理已切换')
    } catch (error: any) {
      toast.error(error?.message || '切换代理失败')
    }
  }

  const handleProxyDeletedFromPicker = (deletedProxyId: string, nextProxies: BrowserProxy[]) => {
    updateProxiesState(nextProxies)
    if (!proxyPickerProfile || proxyPickerProfile.proxyId !== deletedProxyId) return
    const fallbackProxy = nextProxies.find(proxy => proxy.proxyId === directProxyID || proxy.proxyConfig === 'direct://')
    if (fallbackProxy) {
      void saveProfileProxy(proxyPickerProfile, fallbackProxy)
    }
  }

  const proxyErrorCount = proxies.filter(proxy => proxy.lastTestedAt && proxy.lastTestOk === false).length
  const selectedRunningCount = profiles.filter(profile => selectedIds.has(profile.profileId) && profile.running).length
  const inputSyncRunning = inputSyncStatus?.running === true
  const inputSyncReadyTargets = inputSyncStatus?.targets?.filter(target => target.ready).length || 0

  return (
    <div className="h-full overflow-auto pb-5 animate-fade-in">
      <div className="space-y-4">
        <BrowserListHeader
          profileCount={profiles.length}
          filteredProfileCount={filteredProfiles.length}
          runningCount={runningCount}
          errorProfileCount={proxyErrorCount}
          viewMode={viewMode}
          proxies={proxies}
          cores={cores}
          allTags={allTags}
          filters={filters}
          onFiltersChange={setFilters}
          onRefresh={() => { void loadProfiles() }}
          onImportProfiles={handleImportProfiles}
          onOpenBackup={() => setBackupModalOpen(true)}
          importingProfiles={profilePackageBusy}
          onViewModeChange={setViewMode}
        />

        {/* 批量操作工具栏 */}
        <BatchToolbar
          selectedCount={selectedIds.size}
          selectedRunningCount={selectedRunningCount}
          totalCount={filteredProfiles.length}
          onSelectAll={handleSelectAll}
          onDeselectAll={handleDeselectAll}
          onBatchStart={handleBatchStart}
          onRetileRunning={handleRetileRunning}
          onStartInputSync={() => { void handleStartInputSync() }}
          onStopInputSync={() => { void handleStopInputSync() }}
          onRunSpecialInputSync={() => { void handleRunSpecialInputSync() }}
          onBatchStop={handleBatchStop}
          onBatchExport={handleBatchExport}
          onOpenBackup={() => setBackupModalOpen(true)}
          onBatchDelete={openBatchDeleteConfirm}
          batchBusy={batchLoading}
          batchStarting={batchStarting}
          batchStopping={batchStopping}
          retileLoading={retileLoading}
          inputSyncRunning={inputSyncRunning}
          inputSyncLoading={inputSyncLoading}
          exporting={profilePackageBusy}
        />

        {inputSyncRunning && (
          <div className="flex flex-wrap items-center gap-2 border border-[var(--color-border-muted)] bg-[var(--color-bg-subtle)] px-3 py-2 text-xs text-[var(--color-text-secondary)]">
            <span className="font-semibold text-[var(--color-text-primary)]">输入同步运行中</span>
            <span>主控：{inputSyncStatus?.masterName || inputSyncStatus?.masterProfileId}</span>
            <span>目标：{inputSyncReadyTargets}</span>
            <span>事件：{inputSyncStatus?.eventCount || 0}</span>
            {inputSyncStatus?.lastEventAt && <span>最近：{new Date(inputSyncStatus.lastEventAt).toLocaleTimeString()}</span>}
          </div>
        )}

      <BrowserBackupModal
        open={backupModalOpen}
        runningCount={runningCount}
        selectedCount={selectedIds.size}
        selectedExporting={profilePackageBusy}
        loadingMode={backupLoadingMode}
        onClose={() => setBackupModalOpen(false)}
        onExportSelected={() => { void handleBatchExport() }}
        onExportFull={(password) => { void handleExportFullBackup(password) }}
        onImportMerge={(password) => { void handleImportFullBackup(false, password) }}
        onImportReset={(password) => { void handleImportFullBackup(true, password) }}
      />

      <BrowserProfilesPanel
        loading={loading}
        viewMode={viewMode}
        profiles={filteredProfiles}
        proxies={proxies}
        selectedIds={selectedIds}
        resolveProfileCore={resolveProfileCore}
        getProfileCoreLabel={getProfileCoreLabel}
        getProfileStatus={getProfileStatus}
        isProfileStarting={isProfileStarting}
        isProfileStopping={isProfileStopping}
        isProfileBusy={isProfileBusy}
        onToggleSelect={toggleSelect}
        onSelectAll={handleSelectAll}
        onDeselectAll={handleDeselectAll}
        onRefreshProfiles={() => { void loadProfiles() }}
        onStart={(profileId) => { void handleStart(profileId) }}
        onStop={(profileId) => { void handleStop(profileId) }}
        onFocus={(profileId) => { void handleFocus(profileId) }}
        onRestart={(profileId) => { void handleRestart(profileId) }}
        onOpenExtensions={openExtensionModal}
        onExport={(profile) => { void handleExportProfile(profile) }}
        onOpenCopy={openCopyModal}
        onOpenProxyPicker={setProxyPickerProfile}
        onDelete={openDeleteConfirm}
      />

      <ProxyPickerModal
        open={!!proxyPickerProfile}
        currentProxyId={proxyPickerProfile?.proxyId || directProxyID}
        title={proxyPickerProfile ? `切换代理：${proxyPickerProfile.profileName}` : '切换代理'}
        onSelect={(proxy) => {
          if (proxyPickerProfile) {
            void saveProfileProxy(proxyPickerProfile, proxy)
          }
        }}
        onProxyListUpdated={updateProxiesState}
        onProxyDeleted={handleProxyDeletedFromPicker}
        onClose={() => setProxyPickerProfile(null)}
      />

      <ProfileExtensionModal
        open={extensionModal.open}
        profile={extensionModal.profile}
        onClose={closeExtensionModal}
      />

      <BrowserListDialogs
        proxyErrorModal={proxyErrorModal}
        pendingStartId={pendingStartId}
        proxyErrorMsg={proxyErrorMsg}
        onCloseProxyError={() => {
          setProxyErrorModal(false)
          setPendingStartId(null)
        }}
        onStartDirect={() => {
          if (pendingStartId) {
            void handleStartDirect(pendingStartId)
          }
        }}
        startingDirect={pendingStartId ? startingIds.has(pendingStartId) : false}
        copyModal={copyModal}
        copyName={copyName}
        copyOptions={copyOptions}
        onCopyNameChange={setCopyName}
        onCopyOptionsChange={setCopyOptions}
        onCloseCopy={closeCopyModal}
        onConfirmCopy={() => copyModal.profile && handleCopy(copyModal.profile.profileId)}
        copyConfirmDisabled={copyConfirmDisabled}
        copying={copying}
        deleteConfirm={deleteConfirm}
        deleting={batchLoading}
        onCloseDeleteConfirm={closeDeleteConfirm}
        onConfirmDelete={() => { void handleConfirmDelete() }}
        opError={opError}
        onCloseOpError={() => setOpError('')}
      />
      </div>
    </div>
  )
}
