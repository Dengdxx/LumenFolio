const workspace = require("../../frame/workspace")

Page({
  data: {
    source: "artifacts",
    pictures: [],
    next: "",
    flash: "",
    sd: "",
    status: "",
    selected: "",
    preview: "",
    busy: false,
    error: "",
  },

  onShow() {
    if (!workspace.current()) {
      wx.redirectTo({ url: "/pages/connect/index" })
      return
    }
    this.refresh()
  },

  onUnload() {
    removeFile(this.thumbPath)
    this.thumbPath = ""
  },

  async refresh() {
    const session = workspace.current()
    this.setData({ busy: true, error: "" })
    try {
      if (session.link.kind === "bluetooth") await this.refreshBluetooth(session)
      else await this.refreshWifi(session)
    } catch (error) {
      this.setData({ error: error.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  async refreshWifi(session) {
    const [storage, status, page] = await Promise.all([
      session.storage(),
      session.status(),
      session.pictures(this.data.source),
    ])
    this.setData({
      flash: storage.flash,
      sd: storage.sd,
      status: status.summary,
      pictures: page.pictures.map((picture) => present(picture, this.data.source)),
      next: page.next,
    })
  },

  /**
   * 蓝牙同一时刻只跑一个 HTTP 请求，并发发出会让整页一起失败。
   * 先拿到列表，再逐项补容量和状态；后两项失败只提示，不清掉已经显示的列表。
   */
  async refreshBluetooth(session) {
    const page = await session.pictures(this.data.source)
    this.setData({
      pictures: page.pictures.map((picture) => present(picture, this.data.source)),
      next: page.next,
    })
    try {
      const storage = await session.storage()
      this.setData({ flash: storage.flash, sd: storage.sd })
    } catch (error) {
      this.setData({ error: error.message })
    }
    try {
      const status = await session.status()
      this.setData({ status: status.summary })
    } catch (error) {
      this.setData({ error: error.message })
    }
  },

  async switchSource(event) {
    this.setData({ source: event.currentTarget.dataset.source, selected: "", preview: "" })
    await this.refresh()
  },

  async more() {
    const session = workspace.current()
    if (!session || !this.data.next || this.data.busy) return
    this.setData({ busy: true })
    try {
      const page = await session.pictures(this.data.source, this.data.next)
      const source = this.data.source
      this.setData({ pictures: this.data.pictures.concat(page.pictures.map((picture) => present(picture, source))), next: page.next })
    } catch (error) {
      this.setData({ error: error.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  async openPicture(event) {
    const session = workspace.current()
    const id = event.currentTarget.dataset.id
    this.setData({ selected: id, busy: true, error: "" })
    try {
      const thumb = await session.thumbnail(this.data.source, id)
      const path = await writeThumbnail(bmp(thumb))
      const previous = this.thumbPath
      this.thumbPath = path
      this.setData({ preview: path })
      if (previous && previous !== path) removeFile(previous)
    } catch (error) {
      this.setData({ preview: "", error: error.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  async display() {
    const session = workspace.current()
    if (!session || !this.data.selected) return
    this.setData({ busy: true, error: "" })
    try {
      await session.display(this.data.source, this.data.selected)
      wx.showToast({ title: "已安排刷新", icon: "none" })
    } catch (error) {
      this.setData({ error: error.message })
    } finally {
      this.setData({ busy: false })
    }
  },

  remove() {
    const picture = this.data.pictures.find((item) => item.id === this.data.selected)
    if (!picture || !picture.removable) return
    wx.showModal({
      title: "删除图片",
      content: `删除${this.data.source === "sd" ? " SD 卡" : " Flash 上传槽"}中的「${picture.name}」？`,
      confirmText: "确认删除",
      success: (result) => { if (result.confirm) this.confirmRemove() },
    })
  },

  async confirmRemove() {
    const session = workspace.current()
    this.setData({ busy: true, error: "" })
    try {
      await session.remove(this.data.source, this.data.selected)
      this.setData({ selected: "", preview: "" })
      await this.refresh()
    } catch (error) {
      this.setData({ error: error.message, busy: false })
    }
  },
})

/** 相框缩略图是 120×160 的 RGB565，图片组件需要 24 位 BMP。 */
function bmp(base64) {
  const raw = wx.base64ToArrayBuffer(base64)
  const pixels = new Uint8Array(raw)
  const width = 120
  const height = 160
  const row = width * 3
  const stride = Math.ceil(row / 4) * 4
  const image = new Uint8Array(54 + stride * height)
  const view = new DataView(image.buffer)
  image.set([0x42, 0x4d])
  view.setUint32(2, image.length, true)
  view.setUint32(10, 54, true)
  view.setUint32(14, 40, true)
  view.setInt32(18, width, true)
  view.setInt32(22, height, true)
  view.setUint16(26, 1, true)
  view.setUint16(28, 24, true)
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const pixel = pixels[(y * width + x) * 2] | (pixels[(y * width + x) * 2 + 1] << 8)
      const red = ((pixel >> 11) & 31) * 255 / 31
      const green = ((pixel >> 5) & 63) * 255 / 63
      const blue = (pixel & 31) * 255 / 31
      const offset = 54 + (height - 1 - y) * stride + x * 3
      image[offset] = blue
      image[offset + 1] = green
      image[offset + 2] = red
    }
  }
  return image.buffer
}

/** data URI 在真机上可能被图片组件拒绝，改写到本地临时文件再用路径显示。 */
function writeThumbnail(data) {
  const path = `${wx.env.USER_DATA_PATH}/frame-thumb-${Date.now()}-${Math.floor(Math.random() * 100000)}.bmp`
  return new Promise((resolve, reject) => {
    wx.getFileSystemManager().writeFile({
      filePath: path,
      data,
      success: () => resolve(path),
      fail: (error) => reject(new Error(error.errMsg || "缩略图暂时无法保存")),
    })
  })
}

function removeFile(path) {
  if (!path) return
  wx.getFileSystemManager().unlink({ filePath: path, fail: () => {} })
}

function present(picture, source) {
  return {
    ...picture,
    removable: (source === "sd" && Boolean(picture.id)) || picture.id === "inbox",
  }
}

