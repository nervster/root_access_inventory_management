import { useEffect, useRef } from 'react'

/**
 * The back camera, shown in the returned ref's <video> while the component is mounted and always
 * released on unmount. onError gets a message to show when there's no camera to use. Cameras need a
 * secure page: https, or localhost during development.
 */
export function useCamera(onError: (message: string) => void, { highResolution = false }: { highResolution?: boolean } = {}) {
  const video = useRef<HTMLVideoElement>(null)
  // The latest callback, so the camera never needs restarting when it changes.
  const errorCallback = useRef(onError)
  useEffect(() => {
    errorCallback.current = onError
  })

  useEffect(() => {
    let stream: MediaStream | undefined
    let stopped = false

    async function start() {
      if (!navigator.mediaDevices?.getUserMedia) {
        errorCallback.current("This browser can't use the camera here. Open the app over https (or localhost).")
        return
      }
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          // For photos, ask for the camera's best picture; phones give what they can (often 1920 or more wide).
          video: highResolution ? { facingMode: 'environment', width: { ideal: 4096 }, height: { ideal: 3072 } } : { facingMode: 'environment' },
          audio: false,
        })
      } catch {
        errorCallback.current('Camera access was blocked. Allow the camera for this site in your browser settings.')
        return
      }
      if (stopped || !video.current) {
        stream.getTracks().forEach((track) => track.stop())
        return
      }
      video.current.srcObject = stream
      await video.current.play().catch(() => {})
    }

    start()
    return () => {
      stopped = true
      stream?.getTracks().forEach((track) => track.stop())
    }
  }, [highResolution])
  return video
}
