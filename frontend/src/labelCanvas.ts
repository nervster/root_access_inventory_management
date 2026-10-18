import QRCode from 'qrcode'

/** What a roll label shows; the same three lines as the on-screen label in LabelsPage. */
export type LabelContent = { barcode: string; name: string; batchCode: string }

// The 50 × 30 mm roll, drawn at the M2's 300 dpi. The M2 print head is 567 px (48 mm) wide, so the
// canvas is that wide and the label's outer 1 mm on each side is cut off.
const dpi = 300
const mm = (v: number) => Math.round((v * dpi) / 25.4)
const pt = (v: number) => Math.round((v * dpi) / 72)
export const labelWidthPx = 567
export const labelHeightPx = mm(30)
const trimLeftPx = (mm(50) - labelWidthPx) / 2

function fitText(ctx: CanvasRenderingContext2D, text: string, maxWidth: number): string {
  if (ctx.measureText(text).width <= maxWidth) return text
  let t = text
  while (t.length > 1 && ctx.measureText(`${t}…`).width > maxWidth) t = t.slice(0, -1)
  return `${t}…`
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
 * Draws a roll label straight onto a canvas, the same layout as the on-screen one: QR code 24 mm square
 * on the left, text on the right. Drawn rather than screenshotted from the page so the QR modules and
 * text are crisp black on white; the printer only prints black or white.
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

  const { modules } = QRCode.create(content.barcode, { errorCorrectionLevel: 'M' })
  const quiet = 2
  const qrSize = mm(24)
  // Whole pixels per module, so no module is blurred by scaling.
  const cell = Math.floor(qrSize / (modules.size + 2 * quiet))
  const qrDrawn = cell * (modules.size + 2 * quiet)
  const qrX = mm(1.5)
  const qrY = Math.round((labelHeightPx - qrDrawn) / 2)
  for (let row = 0; row < modules.size; row++)
    for (let col = 0; col < modules.size; col++)
      if (modules.get(row, col)) ctx.fillRect(qrX + (col + quiet) * cell, qrY + (row + quiet) * cell, cell, cell)

  const textX = qrX + qrDrawn + mm(1)
  const textWidth = mm(50 - 1.5) - textX
  ctx.textBaseline = 'top'
  const barcodeSize = pt(9)
  const nameSize = pt(6.5)
  const batchSize = pt(6)
  const nameLineHeight = Math.round(nameSize * 1.2)

  ctx.font = `${nameSize}px sans-serif`
  const nameLines = wrapTwoLines(ctx, content.name, textWidth)
  const blockHeight = Math.round(barcodeSize * 1.25) + nameLines.length * nameLineHeight + Math.round(batchSize * 1.25)
  let y = Math.round((labelHeightPx - blockHeight) / 2)

  ctx.font = `bold ${barcodeSize}px monospace`
  ctx.fillText(fitText(ctx, content.barcode, textWidth), textX, y)
  y += Math.round(barcodeSize * 1.25)
  ctx.font = `${nameSize}px sans-serif`
  for (const line of nameLines) {
    ctx.fillText(line, textX, y)
    y += nameLineHeight
  }
  ctx.font = `${batchSize}px sans-serif`
  ctx.fillText(fitText(ctx, content.batchCode, textWidth), textX, y)
  return canvas
}
