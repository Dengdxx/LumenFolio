const workspace = require("../../frame/workspace")
const { formatBytes, inspectFirmware } = require("../../frame/protocol")
const firmwareCloud = require("../../frame/firmware")

Page({
  data: {
    files: [],
    selected: "",
    version: "",
    progress: "",
    error: "",
    cloud: "",
    busy: false,
  },

  onShow() {
    if (!workspace.current()) {
      wx.redirectTo({ url: "/pages/connect/index" })
      return
    }
    this.loadCloud()
  },

  async loadCloud() {
    if (!firmwareCloud.cloudReady()) {
      this.setData({ cloud: "当前微信没有云开发能力，可以改从手机文件选择固件。" })
      return
    }
    this.setData({ busy: true, error: "" })
    try {
      const result = await firmwareCloud.listFirmware()
      this.setData({
        files: (result.files || []).map((file) => ({ ...file, sizeText: formatBytes(file.size) })),
        cloud: result.configured ? "" : "云环境还没有配置固件清单。",
      })
    } catch (error) {
      this.setData({ cloud: "云端固件暂时不可用，可以改从手机文件选择。", error: error.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  async chooseCloud(event) {
    const file = this.data.files.find((item) => item.fileID === event.currentTarget.dataset.id)
    if (!file) return
    this.setData({ busy: true, error: "", progress: "正在下载固件…" })
    try {
      const downloaded = await firmwareCloud.downloadFirmware(file.fileID)
      this.useImage(downloaded.data, file.name)
    } catch (error) {
      this.setData({ error: error.message || "固件没有下载下来" })
    } finally {
      this.setData({ busy: false, progress: "" })
    }
  },

  chooseLocal() {
    wx.chooseMessageFile({
      count: 1,
      type: "file",
      extension: ["bin"],
      success: (result) => {
        const file = result.tempFiles && result.tempFiles[0]
        if (!file) return
        wx.getFileSystemManager().readFile({
          filePath: file.path,
          success: (read) => this.useImage(read.data, file.name),
          fail: () => this.setData({ error: "这个固件文件无法读取" }),
        })
      },
    })
  },

  useImage(data, name) {
    try {
      const firmware = inspectFirmware(data)
      this.image = data
      this.setData({ selected: name, version: firmware.version, error: "" })
    } catch (error) {
      this.image = null
      this.setData({ selected: "", version: "", error: error.message })
    }
  },

  confirmUpgrade() {
    if (!this.image) return
    wx.showModal({
      title: "确认升级",
      content: `升级到 ${this.data.version}。升级期间保持相框供电和小程序打开。`,
      confirmText: "开始升级",
      success: (result) => { if (result.confirm) this.upgrade() },
    })
  },

  async upgrade() {
    const session = workspace.current()
    this.setData({ busy: true, error: "" })
    try {
      const version = await session.upgrade(this.image, (progress) => this.setData({ progress }))
      this.setData({ progress: `新固件 ${version} 已启动并确认` })
    } catch (error) {
      this.setData({ error: error.message || "升级没有完成" })
    } finally {
      this.setData({ busy: false })
    }
  },
})
