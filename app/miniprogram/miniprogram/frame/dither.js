/**
 * 把任意尺寸的取景采样映射到六种墨水。
 * 算法顺序和权重与 Windows、Android 共用的 panel 一致。预览用小图，上传才用完整的 1200×1600。
 */

const INKS = [
  [20, 20, 20],
  [245, 245, 240],
  [240, 200, 30],
  [190, 30, 40],
  [25, 70, 170],
  [30, 140, 60],
]
const NIBBLE = [0, 1, 2, 3, 5, 6]
const PAD = 2
const PANEL_WIDTH = 1200
const PANEL_HEIGHT = 1600

const DIFFUSION = {
  floyd: { divisor: 16, taps: [[1, 0, 7], [-1, 1, 3], [0, 1, 5], [1, 1, 1]] },
  atkinson: { divisor: 8, taps: [[1, 0, 1], [2, 0, 1], [-1, 1, 1], [0, 1, 1], [1, 1, 1], [0, 2, 1]] },
  sierraLite: { divisor: 4, taps: [[1, 0, 2], [-1, 1, 1], [0, 1, 1]] },
  burkes: { divisor: 32, taps: [[1, 0, 8], [2, 0, 4], [-2, 1, 2], [-1, 1, 4], [0, 1, 8], [1, 1, 4], [2, 1, 2]] },
  sierraTwo: { divisor: 16, taps: [[1, 0, 4], [2, 0, 3], [-2, 1, 1], [-1, 1, 2], [0, 1, 3], [1, 1, 2], [2, 1, 1]] },
  sierra: { divisor: 32, taps: [[1, 0, 5], [2, 0, 3], [-2, 1, 2], [-1, 1, 4], [0, 1, 5], [1, 1, 4], [2, 1, 2], [-1, 2, 2], [0, 2, 3], [1, 2, 2]] },
  stucki: { divisor: 42, taps: [[1, 0, 8], [2, 0, 4], [-2, 1, 2], [-1, 1, 4], [0, 1, 8], [1, 1, 4], [2, 1, 2], [-2, 2, 1], [-1, 2, 2], [0, 2, 4], [1, 2, 2], [2, 2, 1]] },
  jarvis: { divisor: 48, taps: [[1, 0, 7], [2, 0, 5], [-2, 1, 3], [-1, 1, 5], [0, 1, 7], [1, 1, 5], [2, 1, 3], [-2, 2, 1], [-1, 2, 3], [0, 2, 5], [1, 2, 3], [2, 2, 1]] },
}

let blueNoise = null

function noiseTile() {
  if (!blueNoise) blueNoise = new Uint8Array(wx.base64ToArrayBuffer(require("./blue-noise.js")))
  return blueNoise
}

function nearest(red, green, blue) {
  let best = 0
  let distance = Infinity
  for (let index = 0; index < INKS.length; index += 1) {
    const ink = INKS[index]
    const dr = red - ink[0]
    const dg = green - ink[1]
    const db = blue - ink[2]
    const value = dr * dr * 0.3 + dg * dg * 0.59 + db * db * 0.11
    if (value < distance) {
      distance = value
      best = index
    }
  }
  return best
}

function nearestTwo(red, green, blue, threshold) {
  let first = 0
  let second = 1
  let firstDistance = Infinity
  let secondDistance = Infinity
  for (let index = 0; index < INKS.length; index += 1) {
    const ink = INKS[index]
    const dr = red - ink[0]
    const dg = green - ink[1]
    const db = blue - ink[2]
    const distance = dr * dr * 0.3 + dg * dg * 0.59 + db * db * 0.11
    if (distance < firstDistance) {
      second = first
      secondDistance = firstDistance
      first = index
      firstDistance = distance
    } else if (distance < secondDistance) {
      second = index
      secondDistance = distance
    }
  }
  if (firstDistance !== 0 && threshold < firstDistance / (firstDistance + secondDistance)) return second
  return first
}

/** 上传整幅时才每 80 行让出界面并回报进度；预览走 renderSync，不经过这里。 */
function pause(row, height, onProgress) {
  if (!onProgress || row % 80 !== 0) return null
  onProgress(row / height)
  return new Promise((resolve) => setTimeout(resolve, 0))
}

function diffuseState(pixels, width, height, kernel, serpentine) {
  const stride = width + PAD * 2
  return {
    pixels,
    width,
    taps: kernel.taps,
    tapCount: kernel.taps.length,
    divisor: kernel.divisor,
    serpentine,
    rows: [new Int32Array(stride * 3), new Int32Array(stride * 3), new Int32Array(stride * 3)],
    result: new Uint8Array(width * height),
  }
}

function diffuseRow(state, y) {
  const { pixels, width, taps, tapCount, divisor, serpentine, rows, result } = state
  const reverse = serpentine && y % 2 === 1
  const start = reverse ? width - 1 : 0
  const step = reverse ? -1 : 1
  const current = rows[0]
  const ahead = rows[1]
  const further = rows[2]
  for (let n = 0; n < width; n += 1) {
    const x = start + n * step
    const source = (y * width + x) * 4
    const column = (x + PAD) * 3
    const red = clamp(pixels[source] + Math.trunc(current[column] / divisor))
    const green = clamp(pixels[source + 1] + Math.trunc(current[column + 1] / divisor))
    const blue = clamp(pixels[source + 2] + Math.trunc(current[column + 2] / divisor))
    const inkIndex = nearest(red, green, blue)
    const ink = INKS[inkIndex]
    result[y * width + x] = inkIndex
    const errorRed = red - ink[0]
    const errorGreen = green - ink[1]
    const errorBlue = blue - ink[2]
    for (let tap = 0; tap < tapCount; tap += 1) {
      const item = taps[tap]
      const target = (x + PAD + step * item[0]) * 3
      const row = item[1] === 0 ? current : item[1] === 1 ? ahead : further
      const weight = item[2]
      row[target] += errorRed * weight
      row[target + 1] += errorGreen * weight
      row[target + 2] += errorBlue * weight
    }
  }
  rows[0] = ahead
  rows[1] = further
  rows[2] = current
  current.fill(0)
}

function diffuseSync(pixels, width, height, kernel, serpentine) {
  const state = diffuseState(pixels, width, height, kernel, serpentine)
  for (let y = 0; y < height; y += 1) diffuseRow(state, y)
  return state.result
}

async function diffuse(pixels, width, height, kernel, serpentine, onProgress) {
  const state = diffuseState(pixels, width, height, kernel, serpentine)
  for (let y = 0; y < height; y += 1) {
    diffuseRow(state, y)
    const waiting = pause(y, height, onProgress)
    if (waiting) await waiting
  }
  return state.result
}

function bayerRanks(size) {
  let ranks = new Uint8Array([0])
  for (let span = 1; span < size; span *= 2) {
    const nextWidth = span * 2
    const next = new Uint8Array(nextWidth * nextWidth)
    for (let y = 0; y < span; y += 1) {
      for (let x = 0; x < span; x += 1) {
        const base = ranks[y * span + x] * 4
        next[y * nextWidth + x] = base
        next[y * nextWidth + x + span] = base + 2
        next[(y + span) * nextWidth + x] = base + 3
        next[(y + span) * nextWidth + x + span] = base + 1
      }
    }
    ranks = next
  }
  return ranks
}

const BAYER4 = bayerRanks(4)
const BAYER8 = bayerRanks(8)

function tiledRow(pixels, width, y, tile, size, levels, result) {
  const scale = 1 / levels
  const tileRow = (y % size) * size
  for (let x = 0; x < width; x += 1) {
    const source = (y * width + x) * 4
    const rank = tile[tileRow + (x % size)]
    result[y * width + x] = nearestTwo(pixels[source], pixels[source + 1], pixels[source + 2], (rank + 0.5) * scale)
  }
}

function tiledSync(pixels, width, height, tile, size, levels) {
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) tiledRow(pixels, width, y, tile, size, levels, result)
  return result
}

async function tiled(pixels, width, height, tile, size, levels, onProgress) {
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) {
    tiledRow(pixels, width, y, tile, size, levels, result)
    const waiting = pause(y, height, onProgress)
    if (waiting) await waiting
  }
  return result
}

function randomRow(pixels, width, y, next, result) {
  for (let x = 0; x < width; x += 1) {
    const source = (y * width + x) * 4
    result[y * width + x] = nearestTwo(pixels[source], pixels[source + 1], pixels[source + 2], next())
  }
}

function noiseSource() {
  let state = 1
  return () => {
    state ^= state << 13
    state ^= state >>> 17
    state ^= state << 5
    return (state >>> 0) / 4294967296
  }
}

function randomNoiseSync(pixels, width, height) {
  const next = noiseSource()
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) randomRow(pixels, width, y, next, result)
  return result
}

async function randomNoise(pixels, width, height, onProgress) {
  const next = noiseSource()
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) {
    randomRow(pixels, width, y, next, result)
    const waiting = pause(y, height, onProgress)
    if (waiting) await waiting
  }
  return result
}

function nearestRow(pixels, width, y, result) {
  let source = y * width * 4
  const row = y * width
  for (let x = 0; x < width; x += 1) {
    result[row + x] = nearest(pixels[source], pixels[source + 1], pixels[source + 2])
    source += 4
  }
}

function nearestPassSync(pixels, width, height) {
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) nearestRow(pixels, width, y, result)
  return result
}

async function nearestPass(pixels, width, height, onProgress) {
  const result = new Uint8Array(width * height)
  for (let y = 0; y < height; y += 1) {
    nearestRow(pixels, width, y, result)
    const waiting = pause(y, height, onProgress)
    if (waiting) await waiting
  }
  return result
}

const ORDERED_KERNELS = [
  DIFFUSION.atkinson,
  DIFFUSION.sierraLite,
  DIFFUSION.burkes,
  DIFFUSION.sierraTwo,
  DIFFUSION.sierra,
  DIFFUSION.stucki,
  DIFFUSION.jarvis,
]

async function render(pixels, width, height, algorithm, onProgress) {
  const source = pixels instanceof Uint8ClampedArray ? pixels : new Uint8ClampedArray(pixels)
  if (algorithm === 0) return nearestPass(source, width, height, onProgress)
  if (algorithm === 1) return diffuse(source, width, height, DIFFUSION.floyd, false, onProgress)
  if (algorithm === 2) return tiled(source, width, height, BAYER4, 4, 16, onProgress)
  if (algorithm === 3) return tiled(source, width, height, BAYER8, 8, 64, onProgress)
  if (algorithm === 4) return randomNoise(source, width, height, onProgress)
  if (algorithm === 5) return diffuse(source, width, height, DIFFUSION.floyd, true, onProgress)
  if (algorithm >= 6 && algorithm <= 12) return diffuse(source, width, height, ORDERED_KERNELS[algorithm - 6], true, onProgress)
  return tiled(source, width, height, noiseTile(), 64, 256, onProgress)
}

/** 预览专用：整幅同步算完，不 await、不 setTimeout，调用方一次拿到结果。 */
function renderSync(pixels, width, height, algorithm) {
  const source = pixels instanceof Uint8ClampedArray ? pixels : new Uint8ClampedArray(pixels)
  if (algorithm === 0) return nearestPassSync(source, width, height)
  if (algorithm === 1) return diffuseSync(source, width, height, DIFFUSION.floyd, false)
  if (algorithm === 2) return tiledSync(source, width, height, BAYER4, 4, 16)
  if (algorithm === 3) return tiledSync(source, width, height, BAYER8, 8, 64)
  if (algorithm === 4) return randomNoiseSync(source, width, height)
  if (algorithm === 5) return diffuseSync(source, width, height, DIFFUSION.floyd, true)
  if (algorithm >= 6 && algorithm <= 12) return diffuseSync(source, width, height, ORDERED_KERNELS[algorithm - 6], true)
  return tiledSync(source, width, height, noiseTile(), 64, 256)
}

function preview(indices, width, height) {
  const image = new Uint8ClampedArray(width * height * 4)
  for (let index = 0; index < indices.length; index += 1) {
    const ink = INKS[indices[index]]
    const offset = index * 4
    image[offset] = ink[0]
    image[offset + 1] = ink[1]
    image[offset + 2] = ink[2]
    image[offset + 3] = 255
  }
  return image
}

function encode(indices) {
  if (indices.length !== PANEL_WIDTH * PANEL_HEIGHT) throw new Error("上传帧必须是 1200×1600")
  const frame = new Uint8Array(12 + PANEL_WIDTH * PANEL_HEIGHT / 2)
  frame.set([0x45, 0x50, 0x44, 0x31])
  frame[8] = PANEL_WIDTH & 255
  frame[9] = PANEL_WIDTH >> 8
  frame[10] = PANEL_HEIGHT & 255
  frame[11] = PANEL_HEIGHT >> 8
  for (let pixel = 0; pixel < indices.length; pixel += 2) {
    frame[12 + pixel / 2] = (NIBBLE[indices[pixel]] << 4) | NIBBLE[indices[pixel + 1]]
  }
  return frame.buffer
}

function clamp(value) {
  if (value < 0) return 0
  if (value > 255) return 255
  return value
}

module.exports = { render, renderSync, preview, encode, PANEL_WIDTH, PANEL_HEIGHT }
