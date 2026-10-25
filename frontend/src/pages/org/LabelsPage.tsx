import QRCode from 'qrcode'
import { useMemo, useState, type CSSProperties } from 'react'
import { useParams } from 'react-router'
import { useBatch, useMembership } from '../../api/hooks'
import type { BatchDto, PlantDto } from '../../api/types'
import { NotFound } from '../../components/Guards'
import { LabelPrinter } from '../../components/LabelPrinter'
import { barcodeSizePt, formatReceived, mono, roll, sans, titleLayout } from '../../labelCanvas'
import { buildXlsx } from '../../labelsExcel'
import { Button, ErrorBanner, Field, Input, LinkButton, Loading, Select } from '../../components/ui'

// Letter paper laid out like 3 × 10 address-label sheets (Avery 5160 size): plain paper can be cut
// apart, and those sticker sheets line up without changes.
const perPage = 30

type Layout = 'roll' | 'letter'

// Remembered per computer, so the one with the label printer opens on the label layout. Storage can
// be blocked (private mode, browser settings); the page still works without it.
const layoutKey = 'plantstock.labelLayout'

function loadLayout(): Layout {
  try {
    return localStorage.getItem(layoutKey) === 'letter' ? 'letter' : 'roll'
  } catch {
    return 'roll'
  }
}

function saveLayout(layout: Layout) {
  try {
    localStorage.setItem(layoutKey, layout)
  } catch {
    // Not remembered; the choice still applies until the page is reloaded.
  }
}

// The Niimbot phone app can't be reached from a phone's browser, but it can import a spreadsheet as a
// label data source, one label per row.
function downloadExcel(plants: PlantDto[], batch: BatchDto) {
  const rows = [
    ['Barcode', 'Plant', 'Batch', 'Received'],
    ...plants.map((p) => [p.barcode, `${p.plantName}${p.variegation ? ` ${p.variegation}` : ''}`, batch.code, formatReceived(batch.receivedOn)]),
  ]
  const blob = new Blob([new Uint8Array(buildXlsx(rows))], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `labels-${batch.code}.xlsx`
  a.click()
  URL.revokeObjectURL(url)
}

/**
 * Printable barcode labels for a batch's living plants. Rendered outside the app's layout so only
 * the labels print. Each label: a QR code holding just the barcode, the barcode text, and the plant.
 */
export function LabelsPage() {
  const params = useParams()
  const orgId = Number(params.orgId)
  const membership = useMembership(orgId)
  const batch = useBatch(orgId, Number(params.batchId))
  const [skip, setSkip] = useState('0')
  const [layout, setLayout] = useState<Layout>(loadLayout)
  const roll = layout === 'roll'

  if (!membership) return <NotFound message="You're not a member of this nursery." />
  if (batch.isPending) return <Loading />
  if (batch.error) return <ErrorBanner error={batch.error} />

  const plants = batch.data.plants.filter((p) => p.status === 'growing' || p.status === 'for_sale')
  // Blank spots first, so a partly used sticker sheet can be reused.
  const blanks = roll ? 0 : Math.min(Math.max(Number(skip) || 0, 0), perPage - 1)
  const cells: (PlantDto | null)[] = [...Array<null>(blanks).fill(null), ...plants]
  const pages = Array.from({ length: Math.ceil(cells.length / perPage) }, (_, i) => cells.slice(i * perPage, (i + 1) * perPage))

  return (
    <div className="bg-white text-black">
      <style>{roll ? `@page { size: 50mm 30mm; margin: 0; }` : `@page { size: letter; margin: 0; }`}</style>

      <div className="mx-auto flex max-w-[8.5in] flex-wrap items-end gap-3 p-4 print:hidden">
        {/* Its own row: beside the controls it was squeezed down to a word per line. */}
        <div className="w-full">
          <h1 className="text-lg font-semibold">
            Labels for {batch.data.batch.code} · {plants.length} plant{plants.length === 1 ? '' : 's'}
          </h1>
          <p className="text-sm text-stone-600">
            {roll
              ? 'In the print dialog, choose the Niimbot printer, paper size 50 × 30 mm, margins None, scale 100%.'
              : 'Letter paper, 30 per page. In the print dialog, set scale to 100% (no "fit to page").'}{' '}
            For the Niimbot app: Export Excel, then in its canvas choose Data source, then Upload via Excel.
          </p>
        </div>
        <div className="w-60">
          <Field label="Layout">
            <Select
              value={layout}
              onChange={(e) => {
                const next = e.target.value as Layout
                setLayout(next)
                saveLayout(next)
              }}
            >
              <option value="roll">Label printer, 50 × 30 mm</option>
              <option value="letter">Letter, 30 per page (Avery 5160)</option>
            </Select>
          </Field>
        </div>
        {!roll && (
          <div className="w-36">
            <Field label="Skip first spots">
              <Input type="number" min={0} max={perPage - 1} value={skip} onChange={(e) => setSkip(e.target.value)} />
            </Field>
          </div>
        )}
        <Button onClick={() => window.print()}>Print</Button>
        <Button variant="secondary" onClick={() => downloadExcel(plants, batch.data.batch)}>
          Export Excel
        </Button>
        <LinkButton variant="secondary" to={`/orgs/${orgId}/batches/${batch.data.batch.id}`}>
          Back
        </LinkButton>
        {roll && (
          <div className="w-full">
            <LabelPrinter
              labels={plants.map((p) => ({
                barcode: p.barcode,
                name: `${p.plantName}${p.variegation ? ` ${p.variegation}` : ''}`,
                batchCode: batch.data.batch.code,
                receivedOn: batch.data.batch.receivedOn,
              }))}
            />
          </div>
        )}
      </div>

      {roll
        ? plants.map((plant) => (
            // One label per page. The last one has no page break after it, or a blank label could feed.
            <section
              key={plant.id}
              className="mx-auto mb-6 box-border h-[30mm] w-[50mm] overflow-hidden break-after-page border border-dashed border-stone-300 print:mb-0 print:border-0 print:last:break-after-auto"
            >
              <RollLabel plant={plant} batch={batch.data.batch} />
            </section>
          ))
        : pages.map((page, i) => (
            <section
              key={i}
              className="mx-auto mb-6 box-border grid h-[11in] w-[8.5in] break-after-page content-start border border-dashed border-stone-300 px-[0.1875in] pt-[0.5in] print:mb-0 print:border-0"
              style={{ gridTemplateColumns: 'repeat(3, 2.625in)', gridAutoRows: '1in', columnGap: '0.125in' }}
            >
              {page.map((plant, j) => (plant ? <Label key={plant.id} plant={plant} batch={batch.data.batch} /> : <div key={`blank-${j}`} />))}
            </section>
          ))}
    </div>
  )
}

/** A label in a 3 × 10 Letter sheet: QR code on the left, text on the right. */
function Label({ plant, batch }: { plant: PlantDto; batch: BatchDto }) {
  return (
    <div className="flex items-center gap-2 overflow-hidden px-2">
      <QR text={plant.barcode} className="size-[0.8in] shrink-0" />
      <div className="min-w-0 leading-tight">
        <p className="line-clamp-2 break-words text-[7.5pt] font-bold">
          {plant.plantName}
          {plant.variegation && ` ${plant.variegation}`}
        </p>
        <p className="font-mono text-[11pt] font-bold">{plant.barcode}</p>
        <p className="text-[7pt] text-stone-600">{batch.code}</p>
        <p className="text-[7pt] text-stone-600">Received {formatReceived(batch.receivedOn)}</p>
      </div>
    </div>
  )
}

/**
 * A 50 × 30 mm roll label, laid out like the Bluetooth one (`roll` and `titleLayout` in labelCanvas): the
 * plant name across the top, sized to fit on two lines, and below it the QR code with the barcode on
 * one line, the batch and the date. All black: grey prints speckled on a thermal printer.
 */
function RollLabel({ plant, batch }: { plant: PlantDto; batch: BatchDto }) {
  const title = titleLayout(`${plant.plantName}${plant.variegation ? ` ${plant.variegation}` : ''}`)
  return (
    <div
      className="box-border flex flex-col overflow-hidden border-black text-black"
      style={{
        margin: `${roll.inset}mm`,
        height: `calc(100% - ${2 * roll.inset}mm)`,
        padding: `${roll.pad}mm`,
        borderWidth: `${roll.border}mm`,
        borderRadius: '2.5mm',
        fontFamily: sans,
      }}
    >
      <p className="font-bold" style={{ fontSize: `${title.sizePt}pt`, lineHeight: roll.titleLineHeight, paddingRight: `${roll.titleRightPad}mm` }}>
        {title.lines.map((line, i) => (
          <span key={i} className="block whitespace-nowrap">
            {line}
          </span>
        ))}
      </p>
      <div className="flex min-h-0 flex-1 items-center" style={{ gap: `${roll.gap}mm`, marginTop: `${roll.gap}mm` }}>
        <QR text={plant.barcode} quiet={roll.quiet} className="shrink-0" style={{ width: `${roll.qr}mm`, height: `${roll.qr}mm` }} />
        <div className="min-w-0 leading-tight" style={{ fontSize: `${roll.smallPt}pt` }}>
          <p className="whitespace-nowrap font-bold" style={{ fontFamily: mono, fontSize: `${barcodeSizePt(plant.barcode)}pt` }}>
            {plant.barcode}
          </p>
          <p className="whitespace-nowrap">{batch.code}</p>
          <p className="whitespace-nowrap">Received {formatReceived(batch.receivedOn)}</p>
        </div>
      </div>
    </div>
  )
}

/** A QR code drawn as SVG squares, so it prints sharp at any size. `quiet` is the white margin, in modules. */
function QR({ text, className, style, quiet = 2 }: { text: string; className?: string; style?: CSSProperties; quiet?: number }) {
  const path = useMemo(() => {
    const { modules } = QRCode.create(text, { errorCorrectionLevel: 'M' })
    let d = ''
    for (let row = 0; row < modules.size; row++)
      for (let col = 0; col < modules.size; col++) if (modules.get(row, col)) d += `M${col} ${row}h1v1h-1z`
    return { d, size: modules.size }
  }, [text])
  return (
    <svg
      className={className}
      style={style}
      viewBox={`${-quiet} ${-quiet} ${path.size + 2 * quiet} ${path.size + 2 * quiet}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label={`QR code for ${text}`}
    >
      <path d={path.d} fill="black" />
    </svg>
  )
}
