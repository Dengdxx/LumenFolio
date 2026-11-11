/** 相框会话上的取景、图库和固件操作。页面不拼 HTTP。 */

const { formatStatus, formatUsage, frameFileName, identityFromBluetooth, inspectFirmware, parseMac } = require("./protocol")
const { decodeText } = require("./http")
const { FrameLink } = require("./link")

const STORAGE = "lumenfolio-frame"

function json(body) {
  const text = decodeText(body)
  return text ? JSON.parse(text) : {}
}

function fail(status, body, fallback) {
  const text = decodeText(body).trim()
  const error = new Error(text || fallback || `相框返回 HTTP ${status}`)
  error.status = status
  return error
}

class FrameSession {
  constructor(link) {
    this.link = link
  }

  static wifi(address) {
    const trimmed = String(address || "").trim()
    const wrapped = trimmed.includes(":") ? trimmed : `${trimmed}:8080`
    const split = wrapped.lastIndexOf(":")
    const host = wrapped.slice(0, split)
    const port = Number(wrapped.slice(split + 1))
    if (!host || port < 1 || port > 65535) throw new Error("相框地址无效")
    return new FrameSession(new FrameLink("wifi", { host, port, name: host }))
  }

  static bluetooth(device) {
    return new FrameSession(new FrameLink("bluetooth", {
      host: "127.0.0.1",
      name: device.name,
      deviceId: device.deviceId,
    }))
  }

  label() {
    return this.link.kind === "bluetooth" ? this.link.name : this.link.host
  }

  close() {
    return this.link.close()
  }

  async status() {
    const response = await this.link.request("GET", "/ota/status")
    if (response.status < 200 || response.status >= 300) throw fail(response.status, response.body, "读不到相框状态")
    const status = json(response.body)
    const reported = parseMac(status.id)
    if (this.link.kind === "bluetooth") {
      const expected = identityFromBluetooth(this.link.deviceId)
      if (reported && expected && reported !== expected) throw new Error("连上了另一台相框")
    }
    return { ...status, summary: formatStatus(status), identity: reported }
  }

  async storage() {
    const response = await this.link.request("GET", "/frames/storage")
    if (response.status === 404) throw new Error("相框固件暂不支持读取容量")
    if (response.status !== 200) throw fail(response.status, response.body)
    const usage = json(response.body)
    return {
      flash: formatUsage(usage.artifacts),
      sd: formatUsage(usage.sd),
      sdMounted: Number(usage.sd && usage.sd.total_bytes) > 0,
    }
  }

  async pictures(source, after = "") {
    const query = { source }
    if (source === "sd") {
      query.limit = "16"
      if (after) query.after = after
    }
    const response = await this.link.request("GET", "/frames", { query })
    if (response.status === 404) throw new Error("相框固件暂不支持浏览图片，请更新固件")
    if (response.status !== 200) throw fail(response.status, response.body)
    const page = json(response.body)
    if (!page.available) throw new Error(source === "sd" ? "SD 卡未挂载" : "Flash artifacts 区不可用")
    return { pictures: page.frames || [], next: page.next || "" }
  }

  async thumbnail(source, id) {
    const response = await this.link.request("GET", "/frame/thumb", { query: { source, id }, timeout: 60000 })
    if (response.status !== 200) throw fail(response.status, response.body, "这张图片暂时没有缩略图")
    return wx.arrayBufferToBase64(response.body)
  }

  async display(source, id) {
    const response = await this.link.request("POST", "/frame/display", { query: { source, id } })
    if (response.status !== 202) throw fail(response.status, response.body, "相框没有安排这张图片")
  }

  async remove(source, id) {
    const response = await this.link.request("DELETE", "/frame", { query: { source, id } })
    if (response.status === 405) throw new Error("相框固件暂不支持删除图片，请更新固件")
    if (response.status !== 204) throw fail(response.status, response.body, "没有删除这张图片")
  }

  /**
   * 上传一张已经编好的帧。小程序不做六色抖动，编码仍由 Windows 或 Android 完成。
   * @param {ArrayBuffer} frame
   * @param {"artifacts"|"sd"} source
   * @param {string} name
   * @param {(sent: number, total: number) => void} [progress]
   */
  async uploadFrame(frame, source, name, progress) {
    const query = { source }
    if (source === "sd") query.id = frameFileName(name)
    const response = await this.link.request("POST", "/frame/transfer", {
      query,
      body: frame,
      timeout: 180000,
      onUpload: progress,
    })
    if ((response.status === 404 || response.status === 405) && source === "artifacts") {
      return this.legacyUpload(frame, progress)
    }
    if (response.status !== 202) throw fail(response.status, response.body, "相框没有接收这张图片")
    const receipt = json(response.body)
    if (!receipt.id) throw new Error("相框已接收画面，但没有返回写屏回执")
    return this.waitTransfer(receipt.id)
  }

  async legacyUpload(frame, progress) {
    const response = await this.link.request("POST", "/frame", { body: frame, timeout: 180000, onUpload: progress })
    if (response.status !== 202) throw fail(response.status, response.body, "旧固件没有接收这张图片")
    return "旧固件未提供 SPI 回执"
  }

  async waitTransfer(id) {
    const deadline = Date.now() + 90000
    while (Date.now() < deadline) {
      const response = await this.link.request("GET", "/frame/transfer", { query: { id: String(id) } })
      if (response.status !== 200) throw fail(response.status, response.body, "写屏回执中断，图片可能已经保存")
      const state = json(response.body)
      if (state.stage === "done" || state.stage === "displayed") return state.message || "已写到屏幕"
      if (state.stage === "failed") throw new Error(state.message || "写屏失败")
      await new Promise((resolve) => setTimeout(resolve, 400))
    }
    throw new Error("写屏回执超时。图片可能已保存，请到相框图片里核对")
  }

  /**
   * 上传并确认一块应用镜像。确认丢失时按相框状态重试，不把同一次升级报成失败。
   * @param {ArrayBuffer} image
   * @param {(message: string) => void} progress
   */
  async upgrade(image, progress) {
    const firmware = inspectFirmware(image)
    progress(`准备升级到 ${firmware.version}`)
    const before = await this.status()
    const response = await this.link.request("POST", "/ota", {
      body: image,
      timeout: 300000,
      onUpload: (sent, total) => progress(`正在上传 ${Math.floor(sent * 100 / total)}%`),
    })
    if (response.status < 200 || response.status >= 300) throw fail(response.status, response.body, "相框没有接收固件")
    const uploaded = json(response.body)
    progress("固件已上传，等待相框重启")
    const deadline = Date.now() + 120000
    while (Date.now() < deadline) {
      const status = await this.status().catch(() => null)
      if (status && status.phase === "rollback_failed") throw new Error("相框回退失败，请改用 USB 重新烧录")
      const matches = status && status.version === firmware.version && status.image_id
        && status.running_partition === (uploaded.update_partition || before.update_partition)
      if (matches && status.ready && status.pending_verify) {
        await this.confirm(status.running_partition, firmware.version, status.image_id).catch(() => {})
      } else if (matches && status.ready && !status.pending_verify && status.phase === "idle") {
        return firmware.version
      }
      await new Promise((resolve) => setTimeout(resolve, 1500))
    }
    throw new Error("新固件尚未确认。保持相框供电，选择同一固件再试")
  }

  confirm(partition, version, imageId) {
    return this.link.request("POST", "/ota/confirm", {
      headers: { "X-OTA-Partition": partition, "X-OTA-Version": version, "X-OTA-Image": imageId },
    }).then((response) => {
      if (response.status < 200 || response.status >= 300) throw fail(response.status, response.body, "没有确认新固件")
    })
  }
}

function rememberedAddress() {
  return wx.getStorageSync(STORAGE) || ""
}

function rememberAddress(address) {
  wx.setStorageSync(STORAGE, address)
}

module.exports = { FrameSession, rememberedAddress, rememberAddress }
