const workspace = require("../../frame/workspace")
const dither = require("../../frame/dither")
const gesture = require("../../frame/gesture")

const PANEL_WIDTH = dither.PANEL_WIDTH
const PANEL_HEIGHT = dither.PANEL_HEIGHT
/**
 * 预览只按面板像素的一半抖动。画布在屏幕上约 343 CSS px 宽，600×800 已经比屏幕密度高，
 * 抖动点每个仍小于一个 CSS 像素；再翻倍到 1200×1600 只多花四倍算力，看不出差别。
 * 上传帧依旧按 PANEL_WIDTH×PANEL_HEIGHT 生成，不受这里影响。
 */
const PREVIEW_DIVISOR = 2
const PREVIEW_WIDTH = PANEL_WIDTH / PREVIEW_DIVISOR
const PREVIEW_HEIGHT = PANEL_HEIGHT / PREVIEW_DIVISOR
const ALGORITHMS = [
  "最近色",
  "Floyd-Steinberg",
  "Bayer 4×4",
  "Bayer 8×8",
  "随机噪声",
  "Floyd-Steinberg（蛇形）",
  "Atkinson",
  "Sierra Lite",
  "Burkes",
  "Sierra Two-Row",
  "Sierra",
  "Stucki",
  "Jarvis–Judice–Ninke",
  "蓝噪声 64×64",
]
const INKS = [
  { name: "黑", color: "#141414" },
  { name: "白", color: "#f5f5f0" },
  { name: "黄", color: "#f0c81e" },
  { name: "红", color: "#be1e28" },
  { name: "蓝", color: "#1946aa" },
  { name: "绿", color: "#1e8c3c" },
]

/** 与 frame/dither.js 的 INKS 同序同值，打包成小端 ABGR 供 Uint32 直接写 ImageData。 */
const INK_PACKED = INKS.map((ink) => {
  const value = parseInt(ink.color.slice(1), 16)
  const red = (value >> 16) & 0xff
  const green = (value >> 8) & 0xff
  const blue = value & 0xff
  return ((0xff << 24) | (blue << 16) | (green << 8) | red) >>> 0
})
const LITTLE_ENDIAN = new Uint8Array(new Uint32Array([1]).buffer)[0] === 1

Page({
  data: {
    connected: false,
    label: "离线",
    picture: "",
    previewMode: false,
    previewing: false,
    algorithm: 0,
    algorithms: ALGORITHMS,
    inks: INKS,
    target: "artifacts",
    sdMounted: false,
    progress: "",
    percent: -1,
    error: "",
    busy: false,
  },

  onLoad() {
    this.crop = { scale: 1, ox: 0, oy: 0, angle: 0, width: 0, height: 0 }
    this.token = 0
    this.samples = {}
    this.previewCache = null
    this.previewImage = null
    this.sampleCanvas = wx.createOffscreenCanvas({ type: "2d", width: PREVIEW_WIDTH, height: PREVIEW_HEIGHT })
  },

  onShow() {
    const session = workspace.current()
    this.setData({ connected: Boolean(session), label: session ? session.label() : "离线" })
    if (session) this.detectStorage()
  },

  async detectStorage() {
    try {
      const storage = await workspace.current().storage()
      this.setData({ sdMounted: storage.sdMounted, target: storage.sdMounted ? "sd" : "artifacts" })
    } catch (error) {
      this.setData({ error: error.message })
    }
  },

  choosePicture() {
    wx.chooseMedia({
      count: 1,
      mediaType: ["image"],
      sizeType: ["original"],
      success: (result) => {
        const file = result.tempFiles && result.tempFiles[0]
        if (!file) return
        wx.getImageInfo({
          src: file.tempFilePath,
          success: (info) => {
            this.token += 1
            this.crop = {
              width: info.width,
              height: info.height,
              scale: Math.min(PANEL_WIDTH / info.width, PANEL_HEIGHT / info.height),
              ox: 0,
              oy: 0,
              angle: 0,
            }
            this.image = null
            this.samples = {}
            this.previewCache = null
            this.preview = null
            this.setData(
              { picture: file.tempFilePath, previewMode: false, previewing: false, error: "", progress: "", percent: -1 },
              () => this.paint()
            )
          },
          fail: () => this.setData({ error: "这张图片无法读取" }),
        })
      },
    })
  },

  setMode(event) {
    const previewMode = event.currentTarget.dataset.mode === "preview"
    this.setData({ previewMode, error: "" })
    if (previewMode) this.makePreview()
    else this.paint()
  },

  toggleMode() {
    if (!this.data.picture) {
      this.choosePicture()
      return
    }
    const previewMode = !this.data.previewMode
    this.setData({ previewMode, error: "" })
    if (previewMode) this.makePreview()
    else this.paint()
  },

  selectAlgorithm(event) {
    this.setData({ algorithm: Number(event.detail.value) })
    if (this.data.previewMode) this.makePreview()
  },

  rotate(event) {
    this.rotateBy(Number(event.currentTarget.dataset.turn) * Math.PI / 180)
    this.afterCropChange()
  },

  reset() {
    const { width, height } = this.crop
    this.crop.scale = Math.min(PANEL_WIDTH / width, PANEL_HEIGHT / height)
    this.crop.ox = 0
    this.crop.oy = 0
    this.crop.angle = 0
    this.afterCropChange()
  },

  chooseTarget(event) {
    const target = event.currentTarget.dataset.target
    if (target === "sd" && !this.data.sdMounted) return
    this.setData({ target })
  },

  touchStart(event) {
    this.measureViewport()
    this.gesture = this.touchPoints(event.touches)
  },

  touchMove(event) {
    if (this.data.previewMode || !this.gesture) return
    const next = this.touchPoints(event.touches)
    if (!next) {
      this.pendingGesture = (event.touches || []).map((touch) => ({ clientX: touch.clientX, clientY: touch.clientY }))
      return
    }
    this.pendingGesture = null
    if (next.length === 1 && this.gesture.length === 1) {
      this.crop = gesture.translate(this.crop, this.gesture[0], next[0], this.viewWidth, this.viewHeight)
    } else if (next.length >= 2 && this.gesture.length >= 2 && this.viewWidth > 0 && this.viewHeight > 0) {
      const before = pair(this.gesture)
      const after = pair(next)
      if (before.distance > 0) {
        const anchor = this.panelPoint(after.x, after.y)
        this.zoom(after.distance / before.distance, anchor.x, anchor.y)
      }
      const raw = after.angle - before.angle
      this.rotateBy(Math.atan2(Math.sin(raw), Math.cos(raw)))
    }
    this.gesture = next
    this.paint()
  },

  touchEnd(event) {
    this.gesture = event.touches && event.touches.length ? this.touchPoints(event.touches) : null
  },

  afterCropChange() {
    if (this.data.previewMode) this.makePreview()
    else this.paint()
  },

  openGallery() {
    wx.switchTab({ url: "/pages/gallery/index" })
  },

  openFirmware() {
    wx.switchTab({ url: "/pages/firmware/index" })
  },

  changeConnection() {
    workspace.close()
    wx.switchTab({ url: "/pages/connect/index" })
  },

  previewKey() {
    const crop = this.crop
    return [this.data.picture, crop.width, crop.height, crop.scale, crop.ox, crop.oy, crop.angle].join("|")
  },

  async makePreview() {
    const token = this.token + 1
    this.token = token
    this.setData({ previewing: true, error: "" })
    try {
      await yieldToPage()
      if (token !== this.token) return
      const key = this.previewKey()
      const algorithm = this.data.algorithm
      const cached = this.previewCache
      if (cached && cached.key === key && cached.algorithm === algorithm) {
        this.preview = cached
        this.setData({ previewing: false })
        await this.paintPreview()
        return
      }
      const pixels = await this.sampleFor(PREVIEW_WIDTH, PREVIEW_HEIGHT)
      if (token !== this.token) return
      await yieldToPage()
      if (token !== this.token) return
      const indices = dither.renderSync(pixels, PREVIEW_WIDTH, PREVIEW_HEIGHT, algorithm)
      if (token !== this.token) return
      this.previewCache = { key, algorithm, indices }
      this.preview = this.previewCache
      this.setData({ previewing: false })
      await this.paintPreview()
    } catch (error) {
      if (token !== this.token) return
      this.setData({ previewing: false, previewMode: false, error: error.message || "六色预览没有完成" })
      this.paint()
    }
  },

  async upload() {
    const session = workspace.current()
    if (!session || !this.data.picture || this.data.busy) return
    const token = this.token
    this.setData({ busy: true, error: "", progress: "正在采样 1200×1600…", percent: 0 })
    try {
      const pixels = await this.sampleFor(PANEL_WIDTH, PANEL_HEIGHT)
      if (token !== this.token) return
      this.setData({ percent: 5, progress: "正在生成六色帧…" })
      await yieldToPage()
      const indices = dither.renderSync(pixels, PANEL_WIDTH, PANEL_HEIGHT, this.data.algorithm)
      if (token !== this.token) return
      const frame = dither.encode(indices)
      this.setData({ percent: 70, progress: "正在上传…" })
      await yieldToPage()
      const message = await session.uploadFrame(frame, this.data.target, fileName(this.data.picture), (sent, total) => {
        this.setData({ percent: 70 + Math.floor(sent * 30 / Math.max(total, 1)), progress: "正在上传…" })
      })
      this.setData({ percent: 100, progress: message || "已保存，屏幕还需要约 35 秒完成刷新" })
    } catch (error) {
      this.setData({ error: error.message || "没有传到相框", progress: "", percent: -1 })
    } finally {
      this.setData({ busy: false })
    }
  },

  rotateBy(delta) {
    const sin = Math.sin(delta)
    const cos = Math.cos(delta)
    const ox = this.crop.ox * cos - this.crop.oy * sin
    const oy = this.crop.ox * sin + this.crop.oy * cos
    this.crop.ox = ox
    this.crop.oy = oy
    this.crop.angle += delta
  },

  zoom(factor, anchorX, anchorY) {
    if (!(factor > 0)) return
    const centerX = PANEL_WIDTH / 2 + this.crop.ox
    const centerY = PANEL_HEIGHT / 2 + this.crop.oy
    const min = 0.05 * Math.min(PANEL_WIDTH / this.crop.width, PANEL_HEIGHT / this.crop.height)
    const max = 50 * Math.max(PANEL_WIDTH / this.crop.width, PANEL_HEIGHT / this.crop.height)
    const next = Math.min(max, Math.max(min, this.crop.scale * factor))
    const applied = next / this.crop.scale
    this.crop.scale = next
    this.crop.ox = anchorX - (anchorX - centerX) * applied - PANEL_WIDTH / 2
    this.crop.oy = anchorY - (anchorY - centerY) * applied - PANEL_HEIGHT / 2
  },

  measureViewport() {
    wx.createSelectorQuery().in(this).select("#viewport").boundingClientRect((rect) => {
      if (!rect || !(rect.width > 0) || !(rect.height > 0)) return
      this.viewWidth = rect.width
      this.viewHeight = rect.height
      this.viewLeft = rect.left
      this.viewTop = rect.top
      if (this.pendingGesture) {
        this.gesture = this.pendingGesture
        this.pendingGesture = null
        this.paint()
      }
    }).exec()
  },

  touchPoints(touches) {
    const points = []
    for (const touch of touches || []) {
      const located = gesture.canvasPoint(touch, {
        left: this.viewLeft,
        top: this.viewTop,
        width: this.viewWidth,
        height: this.viewHeight,
      })
      if (!located) return null
      points.push(located)
    }
    return points.length ? points : null
  },

  panelPoint(x, y) {
    return {
      x: x / this.viewWidth * PANEL_WIDTH,
      y: y / this.viewHeight * PANEL_HEIGHT,
    }
  },

  sample(width, height) {
    if (!this.sampleCanvas || this.sampleCanvas.width !== width || this.sampleCanvas.height !== height) {
      this.sampleCanvas = wx.createOffscreenCanvas({ type: "2d", width, height })
    }
    return this.draw(this.sampleCanvas, width, height, true)
  },

  /** 采样按尺寸分槽缓存：预览用 600×800，上传用 1200×1600，两者互不覆盖。 */
  async sampleFor(width, height) {
    const key = this.previewKey()
    const slot = width + "x" + height
    const hit = this.samples[slot]
    if (hit && hit.key === key) return hit.pixels
    const pixels = await this.sample(width, height)
    this.samples[slot] = { key, pixels }
    return pixels
  },

  paint() {
    if (!this.data.picture || this.data.previewMode) return
    this.canvas("#viewport").then((canvas) => {
      const width = Math.max(2, Math.round((canvas._displayWidth || 360) * 2))
      const height = Math.round(width * 4 / 3)
      return this.draw(canvas, width, height, false)
    }).catch((error) => this.setData({ error: error.message }))
  },

  async paintPreview() {
    if (!this.preview) return
    const canvas = await this.canvas("#viewport")
    if (canvas.width !== PANEL_WIDTH) canvas.width = PANEL_WIDTH
    if (canvas.height !== PANEL_HEIGHT) canvas.height = PANEL_HEIGHT
    const context = canvas.getContext("2d")
    if (!this.previewImage) this.previewImage = context.createImageData(PANEL_WIDTH, PANEL_HEIGHT)
    upsample(this.previewImage.data, this.preview.indices, PREVIEW_WIDTH, PREVIEW_HEIGHT, PREVIEW_DIVISOR)
    context.putImageData(this.previewImage, 0, 0)
  },

  canvas(selector) {
    return new Promise((resolve, reject) => {
      wx.createSelectorQuery().in(this).select(selector).fields({ node: true, size: true }).exec((result) => {
        const found = result && result[0]
        if (!found || !found.node) {
          reject(new Error("取景画布还没有准备好"))
          return
        }
        found.node._displayWidth = found.width
        resolve(found.node)
      })
    })
  },

  draw(canvas, width, height, readPixels) {
    canvas.width = width
    canvas.height = height
    const context = canvas.getContext("2d")
    return this.loadImage(canvas).then((image) => {
      context.fillStyle = "#ffffff"
      context.fillRect(0, 0, width, height)
      context.save()
      const sx = width / PANEL_WIDTH
      const sy = height / PANEL_HEIGHT
      context.translate((PANEL_WIDTH / 2 + this.crop.ox) * sx, (PANEL_HEIGHT / 2 + this.crop.oy) * sy)
      context.rotate(this.crop.angle)
      context.drawImage(
        image,
        -this.crop.width * this.crop.scale * sx / 2,
        -this.crop.height * this.crop.scale * sy / 2,
        this.crop.width * this.crop.scale * sx,
        this.crop.height * this.crop.scale * sy,
      )
      context.restore()
      return readPixels ? context.getImageData(0, 0, width, height).data : null
    })
  },

  loadImage(canvas) {
    if (this.image && this.image.src === this.data.picture) return Promise.resolve(this.image)
    return new Promise((resolve, reject) => {
      const image = canvas.createImage()
      image.onload = () => {
        this.image = image
        resolve(image)
      }
      image.onerror = () => reject(new Error("这张图片无法读取"))
      image.src = this.data.picture
    })
  },
})

/** 让已经 setData 的进度先画出来，再进入下一段长计算。 */
function yieldToPage() {
  return new Promise((resolve) => setTimeout(resolve, 0))
}

/** 把半分辨率的抖动结果按块放大写回整幅 ImageData，省掉对每个面板像素重新求最近色。 */
function upsample(target, indices, width, height, scale) {
  const fullWidth = width * scale
  if (LITTLE_ENDIAN) {
    const out = new Uint32Array(target.buffer, target.byteOffset, target.length >> 2)
    for (let y = 0; y < height; y += 1) {
      for (let x = 0; x < width; x += 1) {
        const color = INK_PACKED[indices[y * width + x]]
        const left = x * scale
        for (let dy = 0; dy < scale; dy += 1) {
          const row = (y * scale + dy) * fullWidth + left
          for (let dx = 0; dx < scale; dx += 1) out[row + dx] = color
        }
      }
    }
    return
  }
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const color = INK_PACKED[indices[y * width + x]]
      const red = color & 0xff
      const green = (color >>> 8) & 0xff
      const blue = (color >>> 16) & 0xff
      for (let dy = 0; dy < scale; dy += 1) {
        for (let dx = 0; dx < scale; dx += 1) {
          const offset = ((y * scale + dy) * fullWidth + x * scale + dx) * 4
          target[offset] = red
          target[offset + 1] = green
          target[offset + 2] = blue
          target[offset + 3] = 255
        }
      }
    }
  }
}

function pair(touches) {
  const dx = touches[1].x - touches[0].x
  const dy = touches[1].y - touches[0].y
  return {
    x: (touches[0].x + touches[1].x) / 2,
    y: (touches[0].y + touches[1].y) / 2,
    distance: Math.hypot(dx, dy),
    angle: Math.atan2(dy, dx),
  }
}

function fileName(path) {
  return (path.split(/[/\\]/).pop() || "photo").replace(/\.[^.]+$/, "") || "photo"
}
