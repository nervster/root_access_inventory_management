import QRCode from 'qrcode'
import { useMemo, useState } from 'react'
import { useParams } from 'react-router'
import { useBatch, useMembership } from '../../api/hooks'
import type { BatchDto, PlantDto } from '../../api/types'
import { NotFound } from '../../components/Guards'
import { Button, ErrorBanner, Field, Input, LinkButton, Loading } from '../../components/ui'

// Letter paper laid out like 3 × 10 address-label sheets (Avery 5160 size): plain paper can be cut
// apart, and those sticker sheets line up without changes.
const perPage = 30

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

  if (!membership) return <NotFound message="You're not a member of this nursery." />
  if (batch.isPending) return <Loading />
  if (batch.error) return <ErrorBanner error={batch.error} />

  const plants = batch.data.plants.filter((p) => p.status === 'growing' || p.status === 'for_sale')
  // Blank spots first, so a partly used sticker sheet can be reused.
  const blanks = Math.min(Math.max(Number(skip) || 0, 0), perPage - 1)
  const cells: (PlantDto | null)[] = [...Array<null>(blanks).fill(null), ...plants]
  const pages = Array.from({ length: Math.ceil(cells.length / perPage) }, (_, i) => cells.slice(i * perPage, (i + 1) * perPage))

  return (
    <div className="bg-white text-black">
      <style>{`@page { size: letter; margin: 0; }`}</style>

      <div className="mx-auto flex max-w-[8.5in] flex-wrap items-end gap-3 p-4 print:hidden">
        <div className="min-w-0 flex-1">
          <h1 className="text-lg font-semibold">
            Labels for {batch.data.batch.code} · {plants.length} plant{plants.length === 1 ? '' : 's'}
          </h1>
          <p className="text-sm text-stone-600">Letter paper, 30 per page. In the print dialog, set scale to 100% (no "fit to page").</p>
        </div>
        <div className="w-36">
          <Field label="Skip first spots">
            <Input type="number" min={0} max={perPage - 1} value={skip} onChange={(e) => setSkip(e.target.value)} />
          </Field>
        </div>
        <Button onClick={() => window.print()}>Print</Button>
        <LinkButton variant="secondary" to={`/orgs/${orgId}/batches/${batch.data.batch.id}`}>
          Back
        </LinkButton>
      </div>

      {pages.map((page, i) => (
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

function Label({ plant, batch }: { plant: PlantDto; batch: BatchDto }) {
  return (
    <div className="flex items-center gap-2 overflow-hidden px-2">
      <QR text={plant.barcode} className="size-[0.8in] shrink-0" />
      <div className="min-w-0 leading-tight">
        <p className="font-mono text-[11pt] font-bold">{plant.barcode}</p>
        <p className="line-clamp-2 text-[7.5pt]">
          {plant.plantName}
          {plant.variegation && ` ${plant.variegation}`}
        </p>
        <p className="text-[7pt] text-stone-600">{batch.code}</p>
      </div>
    </div>
  )
}

/** A QR code drawn as SVG squares, so it prints sharp at any size. */
function QR({ text, className }: { text: string; className?: string }) {
  const path = useMemo(() => {
    const { modules } = QRCode.create(text, { errorCorrectionLevel: 'M' })
    let d = ''
    for (let row = 0; row < modules.size; row++)
      for (let col = 0; col < modules.size; col++) if (modules.get(row, col)) d += `M${col} ${row}h1v1h-1z`
    return { d, size: modules.size }
  }, [text])
  const quiet = 2 // white margin, in modules, that scanners need around the code
  return (
    <svg
      className={className}
      viewBox={`${-quiet} ${-quiet} ${path.size + 2 * quiet} ${path.size + 2 * quiet}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label={`QR code for ${text}`}
    >
      <path d={path.d} fill="black" />
    </svg>
  )
}
