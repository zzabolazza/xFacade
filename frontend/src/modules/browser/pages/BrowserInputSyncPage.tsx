import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, Keyboard, MousePointer2, RefreshCw, RotateCw, Square } from 'lucide-react'
import { EventsOn } from '../../../wailsjs/runtime/runtime'
import { Button, toast } from '../../../shared/components'
import type { BrowserInputSyncStatus, BrowserInputSyncTargetStatus, BrowserProfile, BrowserWindowTileRequest } from '../types'
import { useBrowserListData } from './browserList/useBrowserListData'
import { resolveActionFeedback } from '../utils/actionErrors'
import { buildBrowserWindowTiles } from '../utils/windowTiling'
import {
  fetchBrowserInputSyncStatus,
  focusBrowserInstance,
  resumeBrowserInputSyncTarget,
  resumeRecoverableBrowserInputSyncTargets,
  runBrowserInputSyncSpecialMode,
  setBrowserInstancesWindowBounds,
  setBrowserInstanceWindowBounds,
  startBrowserInputSync,
  stopBrowserInputSync,
} from '../api'

interface BrowserRetileResult {
  success: number
  failed: number
  failureMessages: string[]
  notReadyProfileIds: Set<string>
}

const INPUT_SYNC_MASTER_STORAGE_KEY = 'browser:inputSync:lastMasterId'

function loadLastInputSyncMasterId(): string {
  if (typeof window === 'undefined' || !window.localStorage) return ''
  try {
    return (window.localStorage.getItem(INPUT_SYNC_MASTER_STORAGE_KEY) || '').trim()
  } catch {
    return ''
  }
}

function saveLastInputSyncMasterId(profileId: string) {
  if (typeof window === 'undefined' || !window.localStorage) return
  const id = profileId.trim()
  if (!id) return
  try {
    window.localStorage.setItem(INPUT_SYNC_MASTER_STORAGE_KEY, id)
  } catch {
    // Ignore quota / private-mode failures.
  }
}

function pickPreferredMasterId(
  candidates: BrowserProfile[],
  preferredIds: Array<string | undefined>,
): string {
  for (const preferred of preferredIds) {
    const id = (preferred || '').trim()
    if (!id) continue
    if (candidates.some(profile => profile.profileId === id)) return id
  }
  return candidates[0]?.profileId || ''
}

function formatInputSyncPauseKind(kind?: string): string {
  switch (kind) {
    case 'challenge':
      return '验证码/风控'
    case 'cdp_disconnected':
      return 'CDP 连接'
    case 'dom_mismatch':
      return 'DOM 定位'
    case 'url_deviation':
      return 'URL 偏离'
    case 'page_not_ready':
      return '页面加载'
    case 'action_failures':
      return '连续失败'
    default:
      return '暂停'
  }
}

function formatInputSyncRecoverTime(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleTimeString()
}

function orderTilingTargetsByMaster(
  targets: BrowserProfile[],
  masterProfileId: string | undefined,
): BrowserProfile[] {
  const masterId = (masterProfileId || '').trim()
  if (!masterId) return targets
  const masterIndex = targets.findIndex(profile => profile.profileId === masterId)
  if (masterIndex <= 0) return targets
  return [
    targets[masterIndex],
    ...targets.slice(0, masterIndex),
    ...targets.slice(masterIndex + 1),
  ]
}

function buildWindowTileRequests(targets: BrowserProfile[]): BrowserWindowTileRequest[] {
  const tiles = buildBrowserWindowTiles(targets.length)
  return targets.map((profile, index) => {
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
}

function targetWarningText(target: BrowserInputSyncTargetStatus): string {
  if (target.paused) return `已暂停：${target.pauseReason || target.warning || '需要人工处理'}`
  if (target.waitingForPageLoad) return `等待页面加载：${target.pageLoadWaitReason || target.warning || '页面未就绪'}`
  return target.warning || ''
}

export function BrowserInputSyncPage() {
  const {
    profiles,
    loading,
    mergeProfileState,
    loadProfiles,
  } = useBrowserListData()
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [masterId, setMasterId] = useState(() => loadLastInputSyncMasterId())
  const [retileLoading, setRetileLoading] = useState(false)
  const [inputSyncLoading, setInputSyncLoading] = useState(false)
  const [inputSyncStatus, setInputSyncStatus] = useState<BrowserInputSyncStatus | null>(null)

  const runningProfiles = useMemo(
    () => profiles.filter(profile => profile.running),
    [profiles],
  )
  const runningProfileIds = useMemo(
    () => new Set(runningProfiles.map(profile => profile.profileId)),
    [runningProfiles],
  )
  const selectedRunningProfiles = useMemo(
    () => runningProfiles.filter(profile => selectedIds.has(profile.profileId)),
    [runningProfiles, selectedIds],
  )
  const allRunningSelected = runningProfiles.length > 0 && selectedRunningProfiles.length === runningProfiles.length
  const partiallyRunningSelected = selectedRunningProfiles.length > 0 && !allRunningSelected
  const resolvedMaster = selectedRunningProfiles.find(profile => profile.profileId === masterId) || selectedRunningProfiles[0] || null
  const inputSyncRunning = inputSyncStatus?.running === true
  const selectionDisabled = inputSyncLoading || inputSyncRunning
  const readyTargets = inputSyncStatus?.targets?.filter(target => target.ready) || []
  const pausedTargets = inputSyncStatus?.targets?.filter(target => target.paused) || []
  const recoverablePausedTargets = pausedTargets.filter(target => target.recoverable)
  const waitingTargets = inputSyncStatus?.targets?.filter(target => target.waitingForPageLoad) || []
  const warningTargets = inputSyncStatus?.targets?.filter(target => target.warning || target.paused || target.waitingForPageLoad) || []

  const applyInputSyncStatus = (status: BrowserInputSyncStatus) => {
    setInputSyncStatus(status)
    if (status.running && status.masterProfileId) {
      setMasterId(status.masterProfileId)
    }
  }

  useEffect(() => {
    let cancelled = false
    fetchBrowserInputSyncStatus()
      .then((status) => {
        if (!cancelled) applyInputSyncStatus(status)
      })
      .catch(() => {
        if (!cancelled) setInputSyncStatus(null)
      })
    const off = EventsOn('browser:input-sync:status', (status: BrowserInputSyncStatus) => {
      applyInputSyncStatus(status)
    })
    return () => {
      cancelled = true
      off?.()
    }
  }, [])

  useEffect(() => {
    setSelectedIds(prev => {
      let changed = false
      const next = new Set<string>()
      prev.forEach((profileId) => {
        if (runningProfileIds.has(profileId)) {
          next.add(profileId)
        } else {
          changed = true
        }
      })
      return changed ? next : prev
    })
  }, [runningProfileIds])

  useEffect(() => {
    if (masterId) saveLastInputSyncMasterId(masterId)
  }, [masterId])

  useEffect(() => {
    if (resolvedMaster) {
      if (masterId !== resolvedMaster.profileId) setMasterId(resolvedMaster.profileId)
      return
    }
    if (masterId && !selectedIds.has(masterId)) {
      setMasterId('')
    }
  }, [masterId, resolvedMaster, selectedIds])

  const toggleSelect = (profileId: string) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(profileId)) {
        next.delete(profileId)
        if (masterId === profileId) {
          const remaining = runningProfiles.filter(profile => profile.profileId !== profileId && next.has(profile.profileId))
          setMasterId(pickPreferredMasterId(remaining, [loadLastInputSyncMasterId()]))
        }
      } else {
        next.add(profileId)
        if (!masterId) {
          const selected = runningProfiles.filter(profile => next.has(profile.profileId))
          setMasterId(pickPreferredMasterId(selected, [loadLastInputSyncMasterId(), profileId]))
        }
      }
      return next
    })
  }

  const selectAllRunning = () => {
    setSelectedIds(new Set(runningProfiles.map(profile => profile.profileId)))
    setMasterId(pickPreferredMasterId(runningProfiles, [masterId, loadLastInputSyncMasterId()]))
  }

  const clearSelection = () => {
    setSelectedIds(new Set())
    if (!inputSyncRunning) setMasterId('')
  }

  const retileBrowserProfiles = async (targets: BrowserProfile[]): Promise<BrowserRetileResult> => {
    const tileRequests = buildWindowTileRequests(targets)
    const result: BrowserRetileResult = {
      success: 0,
      failed: 0,
      failureMessages: [],
      notReadyProfileIds: new Set<string>(),
    }
    const recordProblem = (profileId: string, profileName: string, message: string, notReady: boolean) => {
      result.failed += 1
      if (notReady) result.notReadyProfileIds.add(profileId)
      result.failureMessages.push(`${profileName || profileId}：${message}`)
    }

    setRetileLoading(true)
    try {
      try {
        const batchResults = await setBrowserInstancesWindowBounds(tileRequests, true)
        if (batchResults) {
          const seenProfileIds = new Set<string>()
          batchResults.forEach((item) => {
            seenProfileIds.add(item.profileId)
            if (item.ready && !item.warning) {
              result.success += 1
            } else {
              recordProblem(
                item.profileId,
                item.profileName || item.profileId,
                item.warning || '平铺后未能读取实际窗口/视口',
                !item.ready,
              )
            }
          })
          for (const profile of targets) {
            if (!seenProfileIds.has(profile.profileId)) {
              recordProblem(profile.profileId, profile.profileName, '平铺结果缺失', true)
            }
          }
          return result
        }
      } catch {
        // Keep the single-window fallback available in non-desktop or partial binding states.
      }

      for (let index = 0; index < targets.length; index += 1) {
        const profile = targets[index]
        const request = tileRequests[index]
        if (!profile || !request) continue
        try {
          const updatedProfile = await setBrowserInstanceWindowBounds(profile.profileId, request.bounds)
          mergeProfileState(updatedProfile)
          result.success += 1
        } catch (error: any) {
          const feedback = resolveActionFeedback(error, '平铺失败')
          recordProblem(profile.profileId, profile.profileName, feedback.message, true)
        }
      }
      return result
    } finally {
      setRetileLoading(false)
    }
  }

  const restoreMasterFocusAfterTiling = async (targets: BrowserProfile[], targetMasterId: string) => {
    if (!targetMasterId || !targets.some(profile => profile.profileId === targetMasterId)) return
    try {
      const result = await focusBrowserInstance(targetMasterId)
      if (result.warning) {
        toast.warning(`主控窗口已放到左上角，但恢复聚焦提示：${result.warning}`)
      }
    } catch (error: any) {
      toast.warning(error?.message || '主控窗口已放到左上角，但恢复聚焦失败')
    }
  }

  const handleStartInputSync = async () => {
    if (selectedRunningProfiles.length < 2) {
      toast.warning('请至少选择 2 个运行中的实例')
      return
    }
    const master = resolvedMaster || selectedRunningProfiles[0]
    const orderedTargets = orderTilingTargetsByMaster(selectedRunningProfiles, master.profileId)
    const targets = orderedTargets.filter(profile => profile.profileId !== master.profileId)
    setInputSyncLoading(true)
    try {
      const retileResult = await retileBrowserProfiles(orderedTargets)
      if (retileResult.notReadyProfileIds.has(master.profileId)) {
        const masterFailure = retileResult.failureMessages.find(message => message.startsWith(`${master.profileName || master.profileId}：`))
        toast.error(`主控浏览器平铺失败，输入同步未启动${masterFailure ? `：\n${masterFailure}` : ''}`)
        await loadProfiles({ silent: true, syncRuntimeState: true })
        return
      }
      if (retileResult.failureMessages.length > 0) {
        const preview = retileResult.failureMessages.slice(0, 3)
        const more = retileResult.failureMessages.length > preview.length ? `\n另有 ${retileResult.failureMessages.length - preview.length} 个实例有平铺提示。` : ''
        toast.warning(`部分实例平铺/视口校正提示：\n${preview.join('\n')}${more}`)
      }
      await restoreMasterFocusAfterTiling(orderedTargets, master.profileId)
      await loadProfiles({ silent: true, syncRuntimeState: true })
      const status = await startBrowserInputSync({
        masterProfileId: master.profileId,
        targetProfileIds: targets.map(profile => profile.profileId),
        mode: 'page',
      })
      applyInputSyncStatus(status)
      const readyCount = status.targets.filter(target => target.ready).length
      toast.success(`输入同步已开启：主控 ${master.profileName}，目标 ${readyCount} 个`)
      const warnings = status.targets.filter(target => target.warning || target.paused)
      if (warnings.length > 0) {
        const preview = warnings.slice(0, 3).map(target => `${target.profileName || target.profileId}：${targetWarningText(target)}`)
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
      applyInputSyncStatus(status)
      toast.success('输入同步已停止')
    } catch (error: any) {
      toast.error(error?.message || '输入同步停止失败')
    } finally {
      setInputSyncLoading(false)
    }
  }

  const handleResumeInputSyncTarget = async (profileId: string) => {
    setInputSyncLoading(true)
    try {
      const status = await resumeBrowserInputSyncTarget(profileId)
      applyInputSyncStatus(status)
      toast.success('已尝试恢复该从浏览器')
    } catch (error: any) {
      toast.warning(error?.message || '恢复从浏览器失败')
      try {
        applyInputSyncStatus(await fetchBrowserInputSyncStatus())
      } catch {}
    } finally {
      setInputSyncLoading(false)
    }
  }

  const handleResumeRecoverableTargets = async () => {
    setInputSyncLoading(true)
    try {
      const status = await resumeRecoverableBrowserInputSyncTargets()
      applyInputSyncStatus(status)
      toast.success('已尝试恢复可恢复的从浏览器')
    } catch (error: any) {
      toast.warning(error?.message || '恢复可恢复目标失败')
      try {
        applyInputSyncStatus(await fetchBrowserInputSyncStatus())
      } catch {}
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
      applyInputSyncStatus(status)
      toast.success('窗口级同步已完成')
    } catch (error: any) {
      toast.warning(error?.message || '窗口级同步暂不可用')
    } finally {
      setInputSyncLoading(false)
    }
  }

  const refreshAll = async () => {
    await loadProfiles({ silent: false, syncRuntimeState: true })
    try {
      applyInputSyncStatus(await fetchBrowserInputSyncStatus())
    } catch {}
  }

  return (
    <div className="h-full overflow-auto pb-5 animate-fade-in">
      <div className="space-y-4 px-5">
        <section>
          <p className="max-w-2xl text-[12.5px] leading-5 text-[var(--color-text-muted)]">
            指定一个主控，其余为从浏览器。主控上的鼠标、键盘、滚动、导航与 Tab 操作会实时同步到从浏览器。
          </p>
        </section>

        <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <div className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-4 py-3">
            <div className="text-[11.5px] font-semibold text-[var(--color-text-muted)]">主控</div>
            <div className="mt-1.5 truncate text-xl font-extrabold tracking-tight text-[var(--color-text-primary)]">
              {inputSyncStatus?.masterName || resolvedMaster?.profileName || '-'}
            </div>
          </div>
          <div className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-4 py-3">
            <div className="text-[11.5px] font-semibold text-[var(--color-text-muted)]">目标</div>
            <div className="mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-text-primary)]">{readyTargets.length}</div>
          </div>
          <div className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-4 py-3">
            <div className="text-[11.5px] font-semibold text-[var(--color-text-muted)]">暂停</div>
            <div className={pausedTargets.length > 0 ? 'mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-warning)]' : 'mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-text-primary)]'}>
              {pausedTargets.length}
            </div>
          </div>
          <div className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-4 py-3">
            <div className="text-[11.5px] font-semibold text-[var(--color-text-muted)]">等待加载</div>
            <div className={waitingTargets.length > 0 ? 'mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-warning)]' : 'mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-text-primary)]'}>
              {waitingTargets.length}
            </div>
          </div>
          <div className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] px-4 py-3">
            <div className="text-[11.5px] font-semibold text-[var(--color-text-muted)]">事件</div>
            <div className="mt-1.5 text-xl font-extrabold tracking-tight text-[var(--color-text-primary)]">
              {inputSyncStatus?.eventCount || 0}
              {inputSyncStatus?.lastEventAt && <span className="ml-2 text-[11px] font-normal text-[var(--color-text-muted)]">{new Date(inputSyncStatus.lastEventAt).toLocaleTimeString()}</span>}
            </div>
          </div>
        </section>

        {warningTargets.length > 0 && (
          <section className="rounded-[10px] border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-[var(--color-text-primary)]">
              <AlertTriangle className="h-4 w-4 text-[var(--color-warning)]" />
              同步提示
            </div>
            <div className="mt-2 space-y-1 text-xs text-[var(--color-text-secondary)]">
              {warningTargets.slice(0, 4).map(target => (
                <div key={target.profileId}>{target.profileName || target.profileId}：{targetWarningText(target)}</div>
              ))}
            </div>
          </section>
        )}

        <section className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)]">
          <div className="flex flex-wrap items-center gap-3 border-b border-[var(--color-border-default)] px-4 py-3">
            <div className="min-w-0 flex-1">
              <h2 className="text-sm font-semibold text-[var(--color-text-primary)]">运行实例</h2>
              <div className="mt-1 text-xs text-[var(--color-text-muted)]">{selectedRunningProfiles.length} / {runningProfiles.length}</div>
            </div>
            <div className="flex flex-wrap justify-end gap-2">
              <Button size="sm" variant="secondary" onClick={() => { void refreshAll() }} disabled={inputSyncLoading || retileLoading}>
                <RefreshCw className="h-4 w-4" />刷新
              </Button>
              <Button
                size="sm"
                variant={inputSyncRunning ? 'secondary' : 'primary'}
                onClick={() => { void handleStartInputSync() }}
                loading={inputSyncLoading || retileLoading}
                disabled={inputSyncRunning || selectedRunningProfiles.length < 2}
              >
                <MousePointer2 className="h-4 w-4" />开始
              </Button>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => { void handleStopInputSync() }}
                loading={inputSyncLoading && inputSyncRunning}
                disabled={!inputSyncRunning || inputSyncLoading}
              >
                <Square className="h-4 w-4" />停止
              </Button>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => { void handleRunSpecialInputSync() }}
                disabled={!inputSyncRunning || inputSyncLoading}
              >
                <Keyboard className="h-4 w-4" />窗口级
              </Button>
            </div>
          </div>

          {loading ? (
            <div className="px-4 py-12 text-center text-sm text-[var(--color-text-muted)]">正在加载实例</div>
          ) : runningProfiles.length === 0 ? (
            <div className="px-4 py-12 text-center text-sm text-[var(--color-text-muted)]">暂无运行中的实例</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[720px] border-collapse text-sm">
                <thead>
                  <tr className="border-b border-[var(--color-border-default)] bg-[var(--color-bg-subtle)] text-left text-[11px] uppercase text-[var(--color-text-muted)]">
                    <th className="w-12 px-4 py-2 font-semibold">
                      <input
                        type="checkbox"
                        className="h-4 w-4 cursor-pointer rounded accent-[var(--color-accent)]"
                        checked={allRunningSelected}
                        disabled={runningProfiles.length === 0 || selectionDisabled}
                        ref={(input) => {
                          if (input) input.indeterminate = partiallyRunningSelected
                        }}
                        onChange={(event) => {
                          if (event.target.checked) selectAllRunning()
                          else clearSelection()
                        }}
                      />
                    </th>
                    <th className="w-20 px-4 py-2 font-semibold">主控</th>
                    <th className="px-4 py-2 font-semibold">实例</th>
                    <th className="w-28 px-4 py-2 font-semibold">CDP</th>
                    <th className="w-40 px-4 py-2 font-semibold">标签</th>
                  </tr>
                </thead>
                <tbody>
                  {runningProfiles.map((profile) => {
                    const selected = selectedIds.has(profile.profileId)
                    const isMaster = resolvedMaster?.profileId === profile.profileId
                    return (
                      <tr key={profile.profileId} className="border-b border-[var(--color-border-muted)] last:border-b-0 hover:bg-[var(--color-bg-muted)]">
                        <td className="px-4 py-3">
                          <input
                            type="checkbox"
                            className="h-4 w-4 cursor-pointer rounded accent-[var(--color-accent)]"
                            checked={selected}
                            disabled={selectionDisabled}
                            onChange={() => toggleSelect(profile.profileId)}
                          />
                        </td>
                        <td className="px-4 py-3">
                          <input
                            type="radio"
                            name="input-sync-master"
                            className="h-4 w-4 accent-[var(--color-accent)]"
                            checked={selected && isMaster}
                            disabled={!selected || selectionDisabled}
                            onChange={() => setMasterId(profile.profileId)}
                          />
                        </td>
                        <td className="px-4 py-3">
                          <div className="truncate font-semibold text-[var(--color-text-primary)]">{profile.profileName || profile.profileId}</div>
                          <div className="mt-0.5 truncate font-mono text-[11px] text-[var(--color-text-muted)]">{profile.profileId}</div>
                        </td>
                        <td className="px-4 py-3 font-mono text-xs text-[var(--color-text-secondary)]">
                          {profile.debugPort > 0 ? `:${profile.debugPort}` : '-'}
                        </td>
                        <td className="px-4 py-3">
                          <div className="flex max-w-[180px] flex-wrap gap-1">
                            {(profile.tags || []).slice(0, 3).map(tag => (
                              <span key={tag} className="rounded-md bg-[var(--color-bg-muted)] px-1.5 py-0.5 text-[11px] text-[var(--color-text-muted)]">{tag}</span>
                            ))}
                            {(profile.tags || []).length === 0 && <span className="text-xs text-[var(--color-text-muted)]">-</span>}
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>

        {pausedTargets.length > 0 && (
          <section className="rounded-[10px] border border-[var(--color-border-default)] bg-[var(--color-bg-surface)]">
            <div className="flex flex-wrap items-center gap-2 border-b border-[var(--color-border-default)] px-4 py-3">
              <h2 className="min-w-0 flex-1 text-sm font-semibold text-[var(--color-text-primary)]">暂停目标</h2>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => { void handleResumeRecoverableTargets() }}
                loading={inputSyncLoading}
                disabled={recoverablePausedTargets.length === 0}
              >
                <RotateCw className="h-3.5 w-3.5" />恢复可恢复目标
              </Button>
            </div>
            <div className="divide-y divide-[var(--color-border-muted)]">
              {pausedTargets.map((target) => {
                const reason = target.pauseReason || target.warning || '输入同步已暂停'
                return (
                  <div key={target.profileId} className="flex flex-col gap-3 px-4 py-3 md:flex-row md:items-start">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="truncate text-sm font-semibold text-[var(--color-text-primary)]">{target.profileName || target.profileId}</span>
                        <span className="rounded-md bg-[var(--color-bg-muted)] px-1.5 py-0.5 text-[11px] text-[var(--color-text-muted)]">
                          {formatInputSyncPauseKind(target.pauseKind)}
                        </span>
                        <span className={target.recoverable ? 'text-xs text-[var(--color-warning)]' : 'text-xs text-[var(--color-error)]'}>
                          {target.recoverable ? '可自动恢复' : '需要人工处理'}
                        </span>
                      </div>
                      <p className="mt-1 text-xs leading-5 text-[var(--color-text-secondary)]">{reason}</p>
                      <div className="mt-2 flex flex-wrap gap-3 text-[11px] text-[var(--color-text-muted)]">
                        <span>失败：{target.failureCount || 0}</span>
                        <span>丢弃：{target.droppedActionCount || 0}</span>
                        <span>恢复尝试：{target.recoverAttemptCount || 0}</span>
                        {target.recoverable && <span>下次自动恢复：{formatInputSyncRecoverTime(target.nextRecoverAt)}</span>}
                      </div>
                    </div>
                    <Button
                      size="sm"
                      variant={target.recoverable ? 'secondary' : 'ghost'}
                      loading={inputSyncLoading}
                      onClick={() => { void handleResumeInputSyncTarget(target.profileId) }}
                    >
                      立即恢复
                    </Button>
                  </div>
                )
              })}
            </div>
          </section>
        )}
      </div>
    </div>
  )
}
