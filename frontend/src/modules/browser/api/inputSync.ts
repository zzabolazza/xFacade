import type { BrowserInputSyncStatus } from '../types'
import { getBindings } from './runtime'

export interface StartBrowserInputSyncRequest {
  masterProfileId: string
  targetProfileIds: string[]
  mode?: 'page'
}

const stoppedStatus: BrowserInputSyncStatus = {
  running: false,
  targetProfileIds: [],
  eventCount: 0,
  targets: [],
}

export async function startBrowserInputSync(request: StartBrowserInputSyncRequest): Promise<BrowserInputSyncStatus> {
  const bindings: any = await getBindings()
  if (bindings?.BrowserInputSyncStart) {
    return await bindings.BrowserInputSyncStart({
      masterProfileId: request.masterProfileId,
      targetProfileIds: request.targetProfileIds,
      mode: request.mode || 'page',
    })
  }
  return {
    ...stoppedStatus,
    running: true,
    sessionId: 'mock-input-sync',
    mode: 'page',
    masterProfileId: request.masterProfileId,
    targetProfileIds: request.targetProfileIds,
    startedAt: new Date().toISOString(),
  }
}

export async function stopBrowserInputSync(): Promise<BrowserInputSyncStatus> {
  const bindings: any = await getBindings()
  if (bindings?.BrowserInputSyncStop) {
    return await bindings.BrowserInputSyncStop()
  }
  return stoppedStatus
}

export async function fetchBrowserInputSyncStatus(): Promise<BrowserInputSyncStatus> {
  const bindings: any = await getBindings()
  if (bindings?.BrowserInputSyncStatus) {
    return await bindings.BrowserInputSyncStatus()
  }
  return stoppedStatus
}

export async function runBrowserInputSyncSpecialMode(): Promise<BrowserInputSyncStatus> {
  const bindings: any = await getBindings()
  if (bindings?.BrowserInputSyncRunSpecialMode) {
    return await bindings.BrowserInputSyncRunSpecialMode()
  }
  throw new Error('窗口级同步需要桌面运行环境')
}
