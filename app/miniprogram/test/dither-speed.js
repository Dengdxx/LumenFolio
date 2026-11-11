const { performance } = require("node:perf_hooks")
global.wx = { base64ToArrayBuffer: (value) => Buffer.from(value, "base64") }
const dither = require("../miniprogram/frame/dither")

function sample(width, height) {
  const pixels = new Uint8ClampedArray(width * height * 4)
  for (let index = 0; index < pixels.length; index += 4) {
    pixels[index] = index & 255
    pixels[index + 1] = (index >> 2) & 255
    pixels[index + 2] = (index >> 4) & 255
    pixels[index + 3] = 255
  }
  return pixels
}

async function time(name, width, height, algorithm) {
  const pixels = sample(width, height)
  const started = performance.now()
  const indices = await dither.render(pixels, width, height, algorithm)
  const elapsed = performance.now() - started
  if (indices.length !== width * height) throw new Error(name)
  console.log(`${name} ${elapsed.toFixed(0)} ms`)
}

async function main() {
  await time("preview nearest", 240, 320, 0)
  await time("preview floyd", 240, 320, 1)
  await time("preview jarvis", 240, 320, 12)
  await time("upload nearest", 1200, 1600, 0)
}

main()
