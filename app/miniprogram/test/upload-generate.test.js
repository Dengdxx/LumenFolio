/**
 * 上传卡在「正在生成六色帧」的回路。
 * 整幅用 renderSync 一次算完，不在每行里 setData；
 * 算完后进度必须能离开这一句，进入上传。
 */
const assert = require("node:assert/strict")
const dither = require("../miniprogram/frame/dither")

function pixels(width, height) {
  const data = new Uint8ClampedArray(width * height * 4)
  for (let index = 0; index < data.length; index += 4) {
    data[index] = index & 255
    data[index + 1] = (index >> 3) & 255
    data[index + 2] = (index >> 5) & 255
    data[index + 3] = 255
  }
  return data
}

function generate(algorithm, paint) {
  const width = dither.PANEL_WIDTH
  const height = dither.PANEL_HEIGHT
  paint({ percent: 5, progress: "正在生成六色帧…" })
  const indices = dither.renderSync(pixels(width, height), width, height, algorithm)
  const frame = dither.encode(indices)
  paint({ percent: 70, progress: "正在上传…" })
  return { indices, frame }
}

function main() {
  const paints = []
  const started = Date.now()
  const { indices, frame } = generate(1, (state) => paints.push(state))
  const elapsed = Date.now() - started
  assert.equal(indices.length, 1200 * 1600)
  assert.equal(frame.byteLength, 12 + 1200 * 1600 / 2)
  assert.deepEqual(paints.map((item) => item.progress), ["正在生成六色帧…", "正在上传…"])
  assert.deepEqual(paints.map((item) => item.percent), [5, 70])
  assert.ok(elapsed < 3000, `整幅 Floyd 用了 ${elapsed} ms`)
  console.log(`generate paints=${paints.length} last=${paints.at(-1).progress} ${elapsed} ms`)
}

main()
