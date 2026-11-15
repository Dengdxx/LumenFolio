const { FrameSession, rememberAddress, rememberedAddress } = require("../../frame/session")
const { watchFrames } = require("../../frame/link")
const workspace = require("../../frame/workspace")

function targetLabel(value) {
  const trimmed = String(value || "").trim()
  if (!trimmed) return ""
  return trimmed.includes(":") ? trimmed : `${trimmed}:8080`
}

Page({
  data: {
    step: "",
    address: "",
    targetLabel: "",
    busy: false,
    devices: [],
    message: "",
    error: "",
    connected: false,
    currentLabel: "",
    keyboardBottom: 0,
  },

  onLoad() {
    const address = rememberedAddress()
    this.setData({ address, targetLabel: targetLabel(address) })
  },

  onShow() {
    const current = workspace.current()
    this.setData({
      connected: Boolean(current),
      currentLabel: current ? current.label() : "",
    })
  },

  choose(event) {
    const step = event.currentTarget.dataset.step
    this.setData({ step, error: "", message: "", keyboardBottom: 0 })
    if (step === "bluetooth") this.refresh()
    else this.stopWatch()
  },

  closeSheet() {
    if (this.data.busy) return
    this.stopWatch()
    this.setData({ step: "", error: "", message: "", keyboardBottom: 0 })
  },

  noop() {},

  onKeyboardHeight(event) {
    const height = Math.max(0, Math.round((event.detail && event.detail.height) || 0))
    this.setData({ keyboardBottom: height ? height + 12 : 0 })
  },

  editAddress(event) {
    const address = event.detail.value
    this.setData({ address, targetLabel: targetLabel(address) })
  },

  async connectWifi() {
    const address = this.data.address.trim()
    if (!address) return
    this.setData({ busy: true, error: "" })
    try {
      const session = FrameSession.wifi(address)
      await session.status()
      rememberAddress(address)
      workspace.open(session)
      this.setData({ step: "", keyboardBottom: 0 })
      wx.switchTab({ url: "/pages/compose/index" })
    } catch (error) {
      this.setData({ error: error.message || "没有连上相框" })
    } finally {
      this.setData({ busy: false })
    }
  },

  onUnload() {
    this.stopWatch()
  },

  onHide() {
    this.stopWatch()
    if (this.data.keyboardBottom) this.setData({ keyboardBottom: 0 })
  },

  async refresh() {
    this.stopWatch()
    this.pick = (this.pick || 0) + 1
    this.setData({ devices: [], message: "正在查找相框。长按重刷键约两秒，只有一台时会直接进入。" })
    const watch = watchFrames((found) => {
      this.setData({ devices: found })
      if (found.length > 1) {
        this.pick += 1
        this.setData({ message: "找到多台相框，请选择一台。" })
        return
      }
      this.connectBluetooth(found[0], this.pick)
    })
    this.watch = watch
    try {
      await watch.ready
      await new Promise((resolve) => {
        this.watchTimer = setTimeout(resolve, 8000)
      })
      if (!this.data.devices.length) {
        this.setData({ message: "没有找到相框。长按重刷键约两秒后再试。系统蓝牙还连着它时先断开，否则相框不会再广播。" })
      }
    } catch (error) {
      this.setData({ error: bluetoothError(error) })
    } finally {
      this.stopWatch()
    }
  },

  async connectSelected(event) {
    const device = this.data.devices.find((item) => item.deviceId === event.currentTarget.dataset.id)
    if (device) await this.connectBluetooth(device)
  },

  async connectBluetooth(device, pick = this.pick) {
    if (this.data.busy) return
    this.setData({ busy: true, error: "", message: "正在连接相框…" })
    let session = null
    try {
      session = FrameSession.bluetooth(device)
      await session.status()
      if (pick !== this.pick) {
        session.close()
        return
      }
      this.stopWatch()
      workspace.open(session)
      session = null
      this.setData({ step: "", keyboardBottom: 0 })
      wx.switchTab({ url: "/pages/compose/index" })
    } catch (error) {
      if (session) session.close()
      this.setData({ error: error.message || "蓝牙连接失败" })
    } finally {
      this.setData({ busy: false })
    }
  },

  stopWatch() {
    clearTimeout(this.watchTimer)
    if (this.watch) this.watch.stop()
    this.watch = null
  },

  enterOffline() {
    this.stopWatch()
    workspace.open(null)
    wx.switchTab({ url: "/pages/compose/index" })
  },

  resumeCurrent() {
    this.stopWatch()
    wx.switchTab({ url: "/pages/compose/index" })
  },

  openQuickGallery() {
    this.stopWatch()
    wx.switchTab({ url: "/pages/gallery/index" })
  },
})

function bluetoothError(error) {
  const message = error.errMsg || error.message || ""
  if (message.includes("auth deny") || message.includes("authorize")) return "请允许使用蓝牙后再查找相框"
  if (message.includes("location")) return "请打开定位后再查找相框"
  if (error.errCode === 10001 || message.includes("not available")) return "请先打开手机蓝牙"
  return message || "没有找到相框"
}
