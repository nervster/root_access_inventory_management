import { ImageEncoder, NiimbotBluetoothClient, PageColorType, LabelType } from '@mmote/niimbluelib'
import { useEffect, useRef, useState } from 'react'
import { drawLabel, type LabelContent } from '../labelCanvas'
import { Button } from './ui'

type Status = 'disconnected' | 'connecting' | 'connected' | 'printing' | 'error'

const statusText: Record<Status, string> = {
  disconnected: 'Printer not connected',
  connecting: 'Connecting…',
  connected: 'Printer connected',
  printing: 'Printing…',
  error: 'Printer error',
}

// Web Bluetooth exists in Chrome and Edge only; Safari, Firefox and every iPhone browser lack it.
const canPrintDirectly = typeof navigator !== 'undefined' && 'bluetooth' in navigator

/**
 * Prints roll labels from the browser to a Niimbot over Bluetooth, so staff skip the Excel export and the
 * phone app. The connection lives in this component: leaving the page disconnects, and the browser asks
 * for the printer again next time (Web Bluetooth requires that).
 */
export function LabelPrinter({ labels }: { labels: LabelContent[] }) {
  const client = useRef<NiimbotBluetoothClient | null>(null)
  const [status, setStatus] = useState<Status>('disconnected')
  const [printerName, setPrinterName] = useState('')
  // Whether a printer is attached, apart from the last outcome (a failed print leaves it attached).
  const [linked, setLinked] = useState(false)
  const [message, setMessage] = useState('')
  const [progress, setProgress] = useState('')
  // Set synchronously, so a double click can't start a second job before the state update lands.
  const busy = useRef(false)

  useEffect(
    () => () => {
      void client.current?.disconnect()
    },
    [],
  )

  async function connect() {
    if (busy.current) return
    busy.current = true
    setStatus('connecting')
    setMessage('')
    try {
      await client.current?.disconnect()
      const c = new NiimbotBluetoothClient()
      c.on('disconnect', () => {
        // Also fires after our own disconnect(); only report a drop for the client still in use.
        if (client.current !== c) return
        client.current = null
        setLinked(false)
        setStatus('disconnected')
        setMessage('The printer disconnected. Check that it is on, then connect again.')
      })
      c.on('printprogress', (e) => setProgress(`Label ${e.page} of ${e.pagesTotal}`))
      const info = await c.connect()
      client.current = c
      setPrinterName(info.deviceName ?? 'Niimbot')
      setLinked(true)
      setStatus('connected')
    } catch (e) {
      client.current = null
      setLinked(false)
      setStatus('error')
      // Closing the Bluetooth picker rejects with NotFoundError; that's a cancel, not a failure.
      if (e instanceof DOMException && e.name === 'NotFoundError') {
        setStatus('disconnected')
      } else {
        setMessage(e instanceof Error ? e.message : 'Could not connect to the printer.')
      }
    } finally {
      busy.current = false
    }
  }

  async function disconnect() {
    const c = client.current
    client.current = null
    setLinked(false)
    setStatus('disconnected')
    setMessage('')
    await c?.disconnect()
  }

  async function print() {
    const c = client.current
    if (!c || !c.isConnected()) {
      setLinked(false)
      setStatus('disconnected')
      setMessage('Connect the printer first.')
      return
    }
    if (busy.current || labels.length === 0) return
    busy.current = true
    setStatus('printing')
    setMessage('')
    setProgress('')
    const taskName = c.getPrintTaskType()
    const meta = c.getModelMetadata()
    let task: ReturnType<typeof c.protocol.newPrintTask> | undefined
    try {
      if (!taskName || !meta) throw new Error('This printer model is not supported.')
      task = c.protocol.newPrintTask(taskName, {
        totalPages: labels.length,
        labelType: LabelType.WithGaps,
        density: meta.densityDefault,
      })
      await task.printInit()
      for (const label of labels) {
        const image = ImageEncoder.encodeCanvas(drawLabel(label), PageColorType.SingleColor, meta.printDirection)
        await task.printPage(image, 1)
        await task.waitForPageFinished()
      }
      await task.waitForFinished()
      setStatus('connected')
      setMessage(`Printed ${labels.length} label${labels.length === 1 ? '' : 's'}.`)
    } catch (e) {
      setStatus('error')
      setMessage(`${e instanceof Error ? e.message : 'Printing failed.'} Check the label roll and cover, then press Print to try again.`)
    } finally {
      await task?.printEnd().catch(() => undefined)
      setProgress('')
      busy.current = false
    }
  }

  if (!canPrintDirectly) {
    return <p className="text-sm text-stone-600">Printing straight to the label printer needs Chrome or Edge on a computer.</p>
  }

  const connected = linked
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-sm" role="status">
        {statusText[status]}
        {connected && printerName && ` · ${printerName}`}
        {progress && ` · ${progress}`}
      </span>
      {connected ? (
        <>
          <Button onClick={print} disabled={status === 'printing' || labels.length === 0}>
            {status === 'printing' ? 'Printing…' : `Print ${labels.length} label${labels.length === 1 ? '' : 's'} to printer`}
          </Button>
          <Button variant="secondary" onClick={connect} disabled={status === 'printing'}>
            Switch printer
          </Button>
          <Button variant="secondary" onClick={disconnect} disabled={status === 'printing'}>
            Disconnect
          </Button>
        </>
      ) : (
        <Button onClick={connect} disabled={status === 'connecting'}>
          {status === 'error' || message ? 'Reconnect printer' : 'Connect printer'}
        </Button>
      )}
      {message && (
        <p className={`w-full text-sm ${status === 'error' ? 'text-red-700' : 'text-stone-600'}`} role={status === 'error' ? 'alert' : undefined}>
          {message}
        </p>
      )}
    </div>
  )
}
