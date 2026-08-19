import type { Dispatch, SetStateAction } from 'react'
import { toast } from '../../../../shared/components'
import {
  deleteBrowserProfile,
  focusBrowserInstance,
  restartBrowserInstance,
  startBrowserInstanceDirectWithParams,
  startBrowserInstanceWithParams,
  stopBrowserInstance,
  validateProxyConfig,
} from '../../api'
import type { BrowserProfile } from '../../types'
import { resolveActionErrorMessage, resolveActionFeedback } from '../../utils/actionErrors'

const maximizedLaunchArgs = ['--start-maximized']

interface UseBrowserProfileActionsOptions {
  profiles: BrowserProfile[]
  setProxyErrorModal: (open: boolean) => void
  setProxyErrorMsg: (message: string) => void
  setPendingStartId: (profileId: string | null) => void
  setOpError: (message: string) => void
  setStartingIds: Dispatch<SetStateAction<Set<string>>>
  setStoppingIds: Dispatch<SetStateAction<Set<string>>>
  updatePendingIds: (
    setter: Dispatch<SetStateAction<Set<string>>>,
    profileId: string,
    active: boolean,
  ) => void
  mergeProfileState: (profile: BrowserProfile | null | undefined) => void
  loadProfiles: (options?: { silent?: boolean; syncRuntimeState?: boolean }) => Promise<BrowserProfile[] | void>
}

export function useBrowserProfileActions({
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
}: UseBrowserProfileActionsOptions) {
  const handleStart = async (profileId: string) => {
    const profile = profiles.find(p => p.profileId === profileId)
    updatePendingIds(setStartingIds, profileId, true)
    try {
      if (profile) {
        const result = await validateProxyConfig(profile.proxyConfig || '', profile.proxyId || '')
        if (!result.supported) {
          setProxyErrorMsg(result.errorMsg)
          setPendingStartId(profileId)
          setProxyErrorModal(true)
          return
        }
      }

      const startedProfile = await startBrowserInstanceWithParams(profileId, {
        extraLaunchArgs: maximizedLaunchArgs,
      })
      mergeProfileState(startedProfile)
      if (startedProfile?.runtimeWarning) {
        toast.warning(startedProfile.runtimeWarning)
      } else {
        toast.success(`实例已启动${startedProfile?.profileName ? `：${startedProfile.profileName}` : ''}`)
      }
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } catch (error: any) {
      const feedback = resolveActionFeedback(error, '实例启动失败')
      if (feedback.tone === 'warning') {
        toast.warning(feedback.message)
      } else {
        toast.error(feedback.message)
      }
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } finally {
      updatePendingIds(setStartingIds, profileId, false)
    }
  }

  const handleStartDirect = async (profileId: string) => {
    updatePendingIds(setStartingIds, profileId, true)
    try {
      const startedProfile = await startBrowserInstanceDirectWithParams(profileId, {
        extraLaunchArgs: maximizedLaunchArgs,
      })
      mergeProfileState(startedProfile)
      setProxyErrorModal(false)
      setPendingStartId(null)
      if (startedProfile?.runtimeWarning) {
        toast.warning(startedProfile.runtimeWarning)
      } else {
        toast.success(`实例已直连启动${startedProfile?.profileName ? `：${startedProfile.profileName}` : ''}`)
      }
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } catch (error: any) {
      setProxyErrorModal(false)
      setPendingStartId(null)
      const feedback = resolveActionFeedback(error, '实例直连启动失败')
      if (feedback.tone === 'warning') {
        toast.warning(feedback.message)
      } else {
        toast.error(feedback.message)
      }
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } finally {
      updatePendingIds(setStartingIds, profileId, false)
    }
  }

  const handleStop = async (profileId: string) => {
    updatePendingIds(setStoppingIds, profileId, true)
    try {
      const stoppedProfile = await stopBrowserInstance(profileId)
      mergeProfileState(stoppedProfile)
      toast.success('实例已停止')
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } catch (error: any) {
      toast.error(resolveActionErrorMessage(error, '实例停止失败'))
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } finally {
      updatePendingIds(setStoppingIds, profileId, false)
    }
  }

  const handleRestart = async (profileId: string) => {
    updatePendingIds(setStoppingIds, profileId, true)
    try {
      const restartedProfile = await restartBrowserInstance(profileId)
      mergeProfileState(restartedProfile)
      toast.success(`实例已重启${restartedProfile?.profileName ? `：${restartedProfile.profileName}` : ''}`)
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } catch (error: any) {
      const feedback = resolveActionFeedback(error, '实例重启失败')
      if (feedback.tone === 'warning') {
        toast.warning(feedback.message)
      } else {
        setOpError(feedback.message)
      }
      await loadProfiles({ silent: true, syncRuntimeState: true })
    } finally {
      updatePendingIds(setStoppingIds, profileId, false)
    }
  }

  const handleFocus = async (profileId: string) => {
    try {
      const result = await focusBrowserInstance(profileId)
      if (result.warning) {
        toast.warning(result.warning)
      } else {
        toast.success('已聚焦实例窗口')
      }
    } catch (error: any) {
      toast.error(resolveActionErrorMessage(error, '聚焦实例失败'))
    }
  }

  const handleDelete = async (profileId: string) => {
    await deleteBrowserProfile(profileId)
    toast.success('配置已删除')
    void loadProfiles()
  }

  return {
    handleStart,
    handleStartDirect,
    handleStop,
    handleRestart,
    handleFocus,
    handleDelete,
  }
}
