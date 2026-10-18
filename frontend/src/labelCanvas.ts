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

const sans = 'system-ui, "Helvetica Neue", Arial, sans-serif'
const mono = 'ui-monospace, Menlo, Consolas, monospace'

function fitText(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (ctx.measureText(text).width <= maxWidth) return text
  let t = text
  while (t.length > 1 && ctx.measureText(`${t}…`).width > maxWidth) t = t.slice(0, -1)
  return `${t}…`
}

/** Shrinks the font, down to a floor, until the text fits; only then cuts it with "…". */
function fillWidth(ctx: CanvasRenderingContext2D, text: string, family: string, weight: string, size: number, maxWidth: number): string {
  for (let s = size; s >= pt(5); s--) {
    ctx.font = `${weight} ${s}px ${family}`
    if (ctx.measureText(text).width <= maxWidth) return text
  }
  return fitText(ctx, text, maxWidth)
}

/** Words onto at most two lines, the second one shortened with "…" if the name is still too long. */
function wrapTwoLines(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string[] {
  const lines: string[] = []
  let line = ''
  const words = text.split(/\s+/).filter(Boolean)
  for (let i = 0; i < words.length; i++) {
    const next = line ? `${line} ${words[i]}` : words[i]
    if (line && ctx.measureText(next).width > maxWidth) {
      if (lines.length === 1) return [lines[0], fitText(ctx, [line, ...words.slice(i)].join(' '), maxWidth)]
      lines.push(line)
      line = words[i]
    } else {
      line = next
    }
  }
  if (line) lines.push(line)
  return lines.map((l) => fitText(ctx, l, maxWidth))
}

/**
 * Draws a roll label straight onto a canvas, the same layout as the on-screen one: a rounded border, the
 * QR code on the left, text on the right. Drawn rather than screenshotted from the page so the QR modules
 * and text are crisp black on white; the printer only prints black or white.
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

  // Kept well inside the label: printers lose a little at the edges and a clipped border looks broken.
  const inset = mm(1.8)
  ctx.lineWidth = 4
  ctx.beginPath()
  ctx.roundRect(inset, inset, mm(50) - 2 * inset, labelHeightPx - 2 * inset, mm(2.5))
  ctx.stroke()

  const { modules } = QRCode.create(content.barcode, { errorCorrectionLevel: 'M' })
  const quiet = 1
  const qrSize = mm(22)
  // Whole pixels per module, so no module is blurred by scaling.
  const cell = Math.floor(qrSize / (modules.size + 2 * quiet))
  const qrDrawn = cell * (modules.size + 2 * quiet)
  const qrX = inset + mm(1.2)
  const qrY = Math.round((labelHeightPx - qrDrawn) / 2)
  for (let row = 0; row < modules.size; row++)
    for (let col = 0; col < modules.size; col++)
      if (modules.get(row, col)) ctx.fillRect(qrX + (col + quiet) * cell, qrY + (row + quiet) * cell, cell, cell)

  const textX = qrX + qrDrawn + mm(0.8)
  const textWidth = mm(50) - inset - mm(1.5) - textX
  ctx.textBaseline = 'top'
  const nameSize = pt(7.5)
  const barcodeSize = pt(8)
  const smallSize = pt(6)
  const nameLineHeight = Math.round(nameSize * 1.2)
  const barcodeHeight = Math.round(barcodeSize * 1.3)
  const smallHeight = Math.round(smallSize * 1.25)

  ctx.font = `bold ${nameSize}px ${sans}`
  const nameLines = wrapTwoLines(ctx, content.name, textWidth)
  const blockHeight = nameLines.length * nameLineHeight + barcodeHeight + 2 * smallHeight
  let y = Math.round((labelHeightPx - blockHeight) / 2)

  for (const line of nameLines) {
    ctx.fillText(line, textX, y)
    y += nameLineHeight
  }
  // The barcode is the one line that must never be cut off, so it shrinks to fit instead.
  const barcode = fillWidth(ctx, content.barcode, mono, 'bold', barcodeSize, textWidth)
  ctx.fillText(barcode, textX, y)
  y += barcodeHeight
  for (const text of [content.batchCode, `Received ${formatReceived(content.receivedOn)}`]) {
    ctx.fillText(fillWidth(ctx, text, sans, '', smallSize, textWidth), textX, y)
    y += smallHeight
  }
  return canvas
}
