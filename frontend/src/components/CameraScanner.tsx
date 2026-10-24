import jsQR from 'jsqr'
import { useEffect, useRef, useState } from 'react'
import { useCamera } from '../lib/camera'
import { Button } from './ui'

const scanEveryMs = 125 // ~8 frames a second: quick to react, light on the phone's battery
const repeatAfterMs = 2000 // ignore the same code for this long, so a label held in view counts once

/**
 * Reads QR codes with the device camera (the back camera on phones) and calls onScan with each
 * code's text. Uses jsQR rather than the browser's BarcodeDetector, which Safari doesn't support.
 * Cameras need a secure page: https, or localhost during development.
 */
export function CameraScanner({ onScan }: { onScan: (text: string) => void }) {
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)

  return (
    <div className="space-y-2">
      {running ? (
        <>
          <CameraView
            onScan={onScan}
            onError={(message) => {
              setError(message)
              setRunning(false)
            }}
          />
          <Button variant="secondary" className="w-full" onClick={() => setRunning(false)}>
            Stop camera
          </Button>
        </>
      ) : (
        <Button
          className="w-full"
          onClick={() => {
            setError(null)
            setRunning(true)
          }}
        >
          Start camera
        </Button>
      )}
      {error && <p className="text-sm text-red-700 dark:text-red-400">{error}</p>}
    </div>
  )
}

/** The live camera picture. Mounted only while scanning, so leaving it always releases the camera. */
function CameraView({ onScan, onError }: { onScan: (text: string) => void; onError: (message: string) => void }) {
  const video = useCamera((message) => onError(`${message} Or type the barcode instead.`))
  // The latest callback, so the scanning loop below never needs restarting when it changes.
  const scanCallback = useRef(onScan)
  useEffect(() => {
    scanCallback.current = onScan
  })

  useEffect(() => {
    const last = { text: '', at: 0 }
    const canvas = document.createElement('canvas')
    const context = canvas.getContext('2d', { willReadFrequently: true })
    const timer = setInterval(() => {
      const v = video.current
      if (!v || !context || v.readyState < v.HAVE_ENOUGH_DATA) return
      canvas.width = v.videoWidth
      canvas.height = v.videoHeight
      context.drawImage(v, 0, 0)
      const code = jsQR(context.getImageData(0, 0, canvas.width, canvas.height).data, canvas.width, canvas.height, {
        inversionAttempts: 'dontInvert', // labels are black on white
      })
      if (!code?.data) return
      const now = Date.now()
      if (code.data === last.text && now - last.at < repeatAfterMs) return
      last.text = code.data
      last.at = now
      navigator.vibrate?.(60)
      scanCallback.current(code.data)
    }, scanEveryMs)
    return () => clearInterval(timer)
  }, [video])

  return (
    <div className="relative overflow-hidden rounded-xl bg-black">
      <video ref={video} className="aspect-[4/3] w-full object-cover" playsInline muted />
      {/* An aiming square; any code in view is read, not just inside it. */}
      <div className="pointer-events-none absolute inset-0 m-auto size-1/2 rounded-xl border-2 border-white/80" />
    </div>
  )
}
