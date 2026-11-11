/**
 * 取景手势。触点必须已经换成画布左上角为原点的坐标，尺寸必须是画布的 CSS 像素。
 * 尺寸还没量到、或位移不是有限数时，保持原构图，避免一次拖动把图片送到无效位置。
 */

const PANEL_WIDTH = 1200
const PANEL_HEIGHT = 1600

function translate(crop, from, to, width, height) {
  if (!(width > 0) || !(height > 0)) return crop
  const dx = to.x - from.x
  const dy = to.y - from.y
  if (!Number.isFinite(dx) || !Number.isFinite(dy)) return crop
  return {
    ...crop,
    ox: crop.ox + dx / width * PANEL_WIDTH,
    oy: crop.oy + dy / height * PANEL_HEIGHT,
  }
}

/** 画布事件用 x/y；普通组件只有 clientX，要减去画布在屏幕上的位置。 */
function canvasPoint(touch, rect) {
  if (Number.isFinite(touch.x) && Number.isFinite(touch.y)) return { x: touch.x, y: touch.y }
  if (!rect || !(rect.width > 0) || !(rect.height > 0)) return null
  if (!Number.isFinite(touch.clientX) || !Number.isFinite(touch.clientY)) return null
  return { x: touch.clientX - rect.left, y: touch.clientY - rect.top }
}

module.exports = { translate, canvasPoint, PANEL_WIDTH, PANEL_HEIGHT }
