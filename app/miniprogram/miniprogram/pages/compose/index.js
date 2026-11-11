const workspace = require("../../frame/workspace")
const dither = require("../../frame/dither")
const gesture = require("../../frame/gesture")

const PANEL_WIDTH = dither.PANEL_WIDTH
const PANEL_HEIGHT = dither.PANEL_HEIGHT
const PREVIEW_WIDTH = 120
const PREVIEW_HEIGHT = 160
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
            this.sampled = null
            this.setData({ picture: file.tempFilePath, previewMode: false, previewing: false, error: "", progress: "", percent: -1 })
            this.paint()
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
    this.setData({ target: event.currentTarget.dataset.target })
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
    wx.navigateTo({ url: "/pages/gallery/index" })
  },

  openFirmware() {
    wx.navigateTo({ url: "/pages/firmware/index" })
  },

  changeConnection() {
    workspace.close()
    wx.redirectTo({ url: "/pages/connect/index" })
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
      const key = this.previewKey()
      let pixels = this.sampled && this.sampled.key === key ? this.sampled.pixels : null
      if (!pixels) {
        pixels = await this.sample(PREVIEW_WIDTH, PREVIEW_HEIGHT)
        if (token !== this.token) return
        this.sampled = { key, pixels }
      }
      const indices = dither.renderSync(pixels, PREVIEW_WIDTH, PREVIEW_HEIGHT, this.data.algorithm)
      if (token !== this.token) return
      this.preview = { pixels: dither.preview(indices, PREVIEW_WIDTH, PREVIEW_HEIGHT), width: PREVIEW_WIDTH, height: PREVIEW_HEIGHT }
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
      const pixels = await this.sample(PANEL_WIDTH, PANEL_HEIGHT)
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
    if (this.viewWidth > 0 && this.viewHeight > 0) return
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

  paint() {
    if (!this.data.picture || this.data.previewMode) return
    this.canvas("#viewport").then((canvas) => {
      const width = Math.max(2, Math.round(canvas._displayWidth || 360))
      const height = Math.round(width * 4 / 3)
      return this.draw(canvas, width, height, false)
    }).catch((error) => this.setData({ error: error.message }))
  },

  async paintPreview() {
    if (!this.preview) return
    const canvas = await this.canvas("#viewport")
    canvas.width = this.preview.width
    canvas.height = this.preview.height
    const context = canvas.getContext("2d")
    const image = context.createImageData(this.preview.width, this.preview.height)
    image.data.set(this.preview.pixels)
    context.putImageData(image, 0, 0)
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
