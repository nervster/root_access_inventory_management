import QRCode from 'qrcode'

/** What a roll label shows; the same lines as the on-screen label in LabelsPage. */
export type LabelContent = { barcode: string; name: string; batchCode: string; receivedOn: string }

const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** "2026-10-06" as "Oct 6, 2026". Read from the text, not a Date, so no time zone can shift the day. */
export function formatReceived(receivedOn: string): string {
  const [y, m, d] = receivedOn.split('-').map(Number)
  return months[m - 1] ? `${months[m - 1]} ${d}, ${y}` : receivedOn
}

// The 50 × 30 mm roll, drawn at the M2's 300 dpi. The M2 print head is 567 px (48 mm) wide, so the
// canvas is that wide and the label's outer 1 mm on each side is cut off.
const dpi = 300
const mm = (v: number) => Math.round((v * dpi) / 25.4)
const pt = (v: number) => Math.round((v * dpi) / 72)
export const labelWidthPx = 567
export const labelHeightPx = mm(30)
const trimLeftPx = (mm(50) - labelWidthPx) / 2

// Not system-ui: on a Mac that's San Francisco, which turns wider at small sizes, so the printed page
// came out wider than the text was measured here (measured at 300 dpi, where it's narrower).
export const sans = '"Helvetica Neue", Helvetica, Arial, sans-serif'
export const mono = 'ui-monospace, Menlo, Consolas, monospace'

/**
 * The roll label's layout, in mm, shared by the printed page and the Bluetooth canvas so both look the
 * same: a rounded border kept well inside the label (printers lose a little at the edges), the plant
 * name across the top like a title, and below it the QR code with the barcode, batch and date beside it.
 */
export const roll = {
  inset: 1.8, // label edge to border
  border: 0.35,
  pad: 1.2, // border to content
  gap: 1, // title to the row below, and QR code to the text beside it
  qr: 15, // QR code with a 1-module margin: about 0.6 mm modules, easy for a phone to scan
  quiet: 1,
  titleLineHeight: 1.15,
  // Room kept on the right of the plant name: the printed page draws small text a little wider than it
  // measures here, and without it long names ran into the border.
  titleRightPad: 2,
  smallPt: 6.5,
}
const contentWidthMm = 50 - 2 * (roll.inset + roll.border + roll.pad)
const textWidthMm = contentWidthMm - roll.qr - roll.gap

// Largest first: the name gets the biggest size at which it fits on two lines across the label.
const titleSizesPt = [8, 7.5, 7, 6.5, 6, 5.5]

let measurer: CanvasRenderingContext2D | null = null
function measureContext(): CanvasRenderingContext2D {
  measurer ??= document.createElement('canvas').getContext('2d')
  if (!measurer) throw new Error('This browser cannot measure label text.')
  return measurer
}

function fitText(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (ctx.measureText(text).width <= maxWidth) return text
  let t = text
  while (t.length > 1 && ctx.measureText(`${t}…`).width > maxWidth) t = t.slice(0, -1)
  return `${t}…`
}

/** Words onto as many lines as they need at the context's current font. */
function wrap(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string[] {
  const lines: string[] = []
  let line = ''
  for (const word of text.split(/\s+/).filter(Boolean)) {
    const next = line ? `${line} ${word}` : word
    if (line && ctx.measureText(next).width > maxWidth) {
      lines.push(line)
      line = word
    } else {
      line = next
    }
  }
  if (line) lines.push(line)
  return lines
}

/**
 * The plant name as the label's title: the largest size (8 pt down to 5.5 pt) at which it fits on two
 * lines across the label. Only a name too long even then has its second line cut with "…".
 */
export function titleLayout(name: string): { sizePt: number; lines: string[] } {
  const ctx = measureContext()
  const maxWidth = mm(contentWidthMm - roll.titleRightPad)
  for (const sizePt of titleSizesPt) {
    ctx.font = `bold ${pt(sizePt)}px ${sans}`
    const lines = wrap(ctx, name, maxWidth)
    if (lines.length <= 2 && lines.every((l) => ctx.measureText(l).width <= maxWidth)) return { sizePt, lines }
  }
  const sizePt = titleSizesPt[titleSizesPt.length - 1]
  ctx.font = `bold ${pt(sizePt)}px ${sans}`
  const [first, ...rest] = wrap(ctx, name, maxWidth)
  return { sizePt, lines: [fitText(ctx, first, maxWidth), ...(rest.length ? [fitText(ctx, rest.join(' '), maxWidth)] : [])] }
}

/** The barcode's size beside the QR code: 8 pt, smaller if needed, so it's always on one line. */
export function barcodeSizePt(barcode: string): number {
  const ctx = measureContext()
  for (let sizePt = 8; sizePt > 5; sizePt -= 0.5) {
    ctx.font = `bold ${pt(sizePt)}px ${mono}`
    if (ctx.measureText(barcode).width <= mm(textWidthMm)) return sizePt
  }
  return 5
}

/**
 * Draws a roll label straight onto a canvas, in the same layout as the printed page (see `roll`). Drawn
 * rather than screenshotted from the page so the QR modules and text are crisp black on white; the
 * printer only prints black or white.
 */
export function drawLabel(content: LabelContent): HTMLCanvasElement {
  const canvas = document.createElement('canvas')
  canvas.width = labelWidthPx
  canvas.height = labelHeightPx
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('This browser cannot draw the label.')
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, canvas.width, canvas.height)
  ctx.translate(-trimLeftPx, 0)
  ctx.fillStyle = '#000'
  ctx.strokeStyle = '#000'
  ctx.textBaseline = 'top'

  const inset = mm(roll.inset)
  ctx.lineWidth = mm(roll.border)
  ctx.beginPath()
  ctx.roundRect(inset, inset, mm(50) - 2 * inset, labelHeightPx - 2 * inset, mm(2.5))
  ctx.stroke()

  const left = mm(roll.inset + roll.border + roll.pad)
  const top = left
  const bottom = labelHeightPx - top

  const title = titleLayout(content.name)
  const titleSize = pt(title.sizePt)
  const titleLineHeight = Math.round(titleSize * roll.titleLineHeight)
  ctx.font = `bold ${titleSize}px ${sans}`
  title.lines.forEach((line, i) => ctx.fillText(line, left, top + i * titleLineHeight))

  // The row below the title: QR code and text, both centred in the space left.
  const rowTop = top + title.lines.length * titleLineHeight + mm(roll.gap)
  const rowHeight = bottom - rowTop

  const { modules } = QRCode.create(content.barcode, { errorCorrectionLevel: 'M' })
  // Whole pixels per module, so no module is blurred by scaling.
  const cell = Math.floor(mm(roll.qr) / (modules.size + 2 * roll.quiet))
  const qrDrawn = cell * (modules.size + 2 * roll.quiet)
  const qrY = rowTop + Math.round((rowHeight - qrDrawn) / 2)
  for (let row = 0; row < modules.size; row++)
    for (let col = 0; col < modules.size; col++)
      if (modules.get(row, col)) ctx.fillRect(left + (col + roll.quiet) * cell, qrY + (row + roll.quiet) * cell, cell, cell)

  const textX = left + qrDrawn + mm(roll.gap)
  const barcodeSize = pt(barcodeSizePt(content.barcode))
  const smallSize = pt(roll.smallPt)
  const barcodeHeight = Math.round(barcodeSize * 1.3)
  const smallHeight = Math.round(smallSize * 1.25)
  let y = rowTop + Math.round((rowHeight - barcodeHeight - 2 * smallHeight) / 2)

  ctx.font = `bold ${barcodeSize}px ${mono}`
  ctx.fillText(content.barcode, textX, y)
  y += barcodeHeight
  ctx.font = `${smallSize}px ${sans}`
  for (const text of [content.batchCode, `Received ${formatReceived(content.receivedOn)}`]) {
    ctx.fillText(fitText(ctx, text, mm(textWidthMm)), textX, y)
    y += smallHeight
  }
  return canvas
}
