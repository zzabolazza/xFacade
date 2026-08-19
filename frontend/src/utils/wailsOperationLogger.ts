const SKIP_METHODS = new Set(['GetAppLogs', 'FrontendOperationLog'])

let installed = false

function stringifyError(error: unknown) {
  if (error instanceof Error) return error.message || String(error)
  if (typeof error === 'string') return error
  try {
    return JSON.stringify(error)
  } catch {
    return String(error)
  }
}

function shouldLogSuccess(_method: string) {
  return false
}

function shouldLogFailure(method: string) {
  return !SKIP_METHODS.has(method)
}

function recordOperation(level: 'info' | 'error', method: string, success: boolean, durationMs: number, message = '') {
  const app = (window as any)?.go?.main?.App
  const logger = app?.FrontendOperationLog
  if (typeof logger !== 'function') return
  try {
    void logger(level, method, success, Math.max(0, Math.round(durationMs)), String(message || '').slice(0, 1200))
  } catch {
    // Logging must never break user operations.
  }
}

export function installWailsOperationLogger() {
  if (installed) return
  installed = true

  const app = (window as any)?.go?.main?.App
  if (!app || typeof app !== 'object') return

  Object.keys(app).forEach((method) => {
    const original = app[method]
    if (typeof original !== 'function' || SKIP_METHODS.has(method)) return

    app[method] = (...args: unknown[]) => {
      const startedAt = performance.now()
      try {
        const result = original(...args)
        if (result && typeof result.then === 'function') {
          return result.then((value: unknown) => {
            if (shouldLogSuccess(method)) {
              recordOperation('info', method, true, performance.now() - startedAt)
            }
            return value
          }).catch((error: unknown) => {
            if (shouldLogFailure(method)) {
              recordOperation('error', method, false, performance.now() - startedAt, stringifyError(error))
            }
            throw error
          })
        }
        if (shouldLogSuccess(method)) {
          recordOperation('info', method, true, performance.now() - startedAt)
        }
        return result
      } catch (error) {
        if (shouldLogFailure(method)) {
          recordOperation('error', method, false, performance.now() - startedAt, stringifyError(error))
        }
        throw error
      }
    }
  })
}
