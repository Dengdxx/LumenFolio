const { FrameSession, rememberAddress, rememberedAddress } = require("../../frame/session")
const { watchFrames } = require("../../frame/link")
const workspace = require("../../frame/workspace")

Page({
  data: {
    step: "",
    address: "",
    busy: false,
    devices: [],
    message: "",
    error: "",
  },

  onLoad() {
    this.setData({ address: rememberedAddress() })
  },

  choose(event) {
    const step = event.currentTarget.dataset.step
    this.setData({ step, error: "", message: "" })
    if (step === "bluetooth") this.refresh()
  },

  editAddress(event) {
    this.setData({ address: event.detail.value })
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
      wx.redirectTo({ url: "/pages/compose/index" })
    } catch (error) {
      this.setData({ error: error.message || "没有连上相框" })
    } finally {
      this.setData({ busy: false })
    }
  },

  onUnload() {
    this.stopWatch()
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
      wx.redirectTo({ url: "/pages/compose/index" })
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
    wx.redirectTo({ url: "/pages/compose/index" })
  },
})

function bluetoothError(error) {
  const message = error.errMsg || error.message || ""
  if (message.includes("auth deny") || message.includes("authorize")) return "请允许使用蓝牙后再查找相框"
  if (message.includes("location")) return "请打开定位后再查找相框"
  if (error.errCode === 10001 || message.includes("not available")) return "请先打开手机蓝牙"
  return message || "没有找到相框"
}
