/**
 * Shrinks a photo before uploading: at most maxSide pixels on its longest side, as JPEG. A 12 MB phone
 * photo becomes roughly 0.5 MB, which matters on weak greenhouse Wi-Fi. Honors the camera's rotation.
 */
export async function shrinkPhoto(file: File, maxSide = 2000, quality = 0.85): Promise<Blob> {
  const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
  const scale = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height))
  const canvas = document.createElement('canvas')
  canvas.width = Math.round(bitmap.width * scale)
  canvas.height = Math.round(bitmap.height * scale)
  canvas.getContext('2d')?.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
  bitmap.close()
  return new Promise((resolve, reject) =>
    canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error("Couldn't read that photo."))), 'image/jpeg', quality),
  )
}
