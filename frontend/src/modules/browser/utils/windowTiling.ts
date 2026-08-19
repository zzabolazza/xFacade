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

interface TilingGrid {
  columns: number
  rows: number
  tileWidth: number
  tileHeight: number
  originX: number
  originY: number
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
  return buildBrowserWindowTilesForBounds(count, resolveAvailableScreenBounds())
}

export function buildBrowserWindowTilesForBounds(count: number, bounds: ScreenBounds): BrowserWindowTile[] {
  const total = Math.max(0, Math.floor(count))
  if (total === 0) return []

  const grid = buildTilingGrid(total, bounds)

  return Array.from({ length: total }, (_, index) => {
    const row = Math.floor(index / grid.columns)
    const column = index - row * grid.columns
    const rowCount = Math.min(grid.columns, total - row * grid.columns)
    const rowOffset = Math.floor(((grid.columns - rowCount) * grid.tileWidth) / 2)
    const x = grid.originX + rowOffset + column * grid.tileWidth
    const y = grid.originY + row * grid.tileHeight

    return {
      x,
      y,
      width: grid.tileWidth,
      height: grid.tileHeight,
      launchArgs: [
        `--window-position=${x},${y}`,
        `--window-size=${grid.tileWidth},${grid.tileHeight}`,
      ],
    }
  })
}

function buildTilingGrid(total: number, bounds: ScreenBounds): TilingGrid {
  const columns = Math.ceil(Math.sqrt(total))
  const rows = Math.ceil(total / columns)
  const tileWidth = Math.max(1, Math.floor(bounds.width / columns))
  const tileHeight = Math.max(1, Math.floor(bounds.height / rows))
  const usedWidth = tileWidth * columns
  const usedHeight = tileHeight * rows

  return {
    columns,
    rows,
    tileWidth,
    tileHeight,
    originX: bounds.left + Math.floor((bounds.width - usedWidth) / 2),
    originY: bounds.top + Math.floor((bounds.height - usedHeight) / 2),
  }
}
