export interface BrowserWindowTile {
  x: number
  y: number
  width: number
  height: number
  launchArgs: string[]
}

interface ScreenBounds {
  left: number
  top: number
  width: number
  height: number
}

function toFiniteNumber(value: unknown, fallback: number): number {
  const numeric = Number(value)
  return Number.isFinite(numeric) ? numeric : fallback
}

function toPositiveInteger(value: unknown, fallback: number): number {
  return Math.max(1, Math.floor(toFiniteNumber(value, fallback)))
}

function resolveAvailableScreenBounds(): ScreenBounds {
  if (typeof globalThis.screen === 'undefined') {
    return { left: 0, top: 0, width: 1200, height: 800 }
  }

  const screenInfo = globalThis.screen as Screen & { availLeft?: number; availTop?: number }
  return {
    left: Math.floor(toFiniteNumber(screenInfo.availLeft, 0)),
    top: Math.floor(toFiniteNumber(screenInfo.availTop, 0)),
    width: toPositiveInteger(screenInfo.availWidth, 1200),
    height: toPositiveInteger(screenInfo.availHeight, 800),
  }
}

export function buildBrowserWindowTiles(count: number): BrowserWindowTile[] {
  const total = Math.max(0, Math.floor(count))
  if (total === 0) return []

  const bounds = resolveAvailableScreenBounds()
  const columns = Math.ceil(Math.sqrt(total))
  const rows = Math.ceil(total / columns)
  const cellWidth = Math.max(1, Math.floor(bounds.width / columns))
  const cellHeight = Math.max(1, Math.floor(bounds.height / rows))

  return Array.from({ length: total }, (_, index) => {
    const column = index % columns
    const row = Math.floor(index / columns)
    const x = bounds.left + column * cellWidth
    const y = bounds.top + row * cellHeight
    const width = column === columns - 1 ? Math.max(1, bounds.width - cellWidth * column) : cellWidth
    const height = row === rows - 1 ? Math.max(1, bounds.height - cellHeight * row) : cellHeight

    return {
      x,
      y,
      width,
      height,
      launchArgs: [
        `--window-position=${x},${y}`,
        `--window-size=${width},${height}`,
      ],
    }
  })
}
