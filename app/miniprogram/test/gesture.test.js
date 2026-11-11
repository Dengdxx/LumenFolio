const assert = require("node:assert/strict")
const { translate, canvasPoint } = require("../miniprogram/frame/gesture")

const crop = { ox: 0, oy: 0, scale: 1, angle: 0 }

const missing = translate(crop, { x: 0, y: 0 }, { x: 30, y: 10 }, undefined, undefined)
assert.equal(missing, crop, "画布尺寸未量到时不能改构图")

const invalid = translate(crop, { x: 0, y: 0 }, { x: Number.NaN, y: 10 }, 300, 400)
assert.equal(invalid, crop, "无效触点不能改构图")

const moved = translate(crop, { x: 10, y: 20 }, { x: 40, y: 60 }, 300, 400)
assert.equal(moved.ox, 30 / 300 * 1200)
assert.equal(moved.oy, 40 / 400 * 1600)
assert.equal(crop.ox, 0, "原构图不被修改")

assert.deepEqual(canvasPoint({ x: 12, y: 8, clientX: 99, clientY: 99 }, null), { x: 12, y: 8 })
assert.equal(canvasPoint({ clientX: 140, clientY: 220 }, null), null)
assert.deepEqual(
  canvasPoint({ clientX: 140, clientY: 220 }, { left: 100, top: 180, width: 300, height: 400 }),
  { x: 40, y: 40 },
)

console.log("gesture ok")
