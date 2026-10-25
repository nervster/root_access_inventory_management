import type { PlantDto } from './api/types'

/** A scanned label: the plant once looked up, or why it couldn't be. */
export interface Scanned {
  barcode: string
  plant?: PlantDto
  problem?: string
}

/**
 * The Scan page's list, kept for this browser tab, so opening a plant (or a photo session) and coming
 * back doesn't lose it.
 */
function storageKey(orgId: number) {
  return `nms.scan.${orgId}`
}

export function loadScanned(orgId: number): Scanned[] {
  try {
    return JSON.parse(sessionStorage.getItem(storageKey(orgId)) ?? '[]') as Scanned[]
  } catch {
    return []
  }
}

export function saveScanned(orgId: number, scanned: Scanned[]) {
  try {
    sessionStorage.setItem(storageKey(orgId), JSON.stringify(scanned))
  } catch {
    // Storage can be unavailable (private browsing); the list still works for this visit.
  }
}
