/**
 * 一台相框的一次会话。Wi-Fi 和蓝牙都到达同一套 HTTP 接口。
 *
 * 蓝牙把一次 HTTP 请求拆成带响应的 GATT 写：1 打开、2 传数据、3 关闭。
 * 相框的读写特征都要求加密。系统是否完成 Just Works 配对由手机决定，
 * 这里不另造配对流程；连接失败时把原因交回页面。
 */

const { binaryHeaders, decodeHttp, requestPath } = require("./http")

const SERVICE = "9FA16700-7E6B-4D75-89C4-5C79B25EAC01"
const WRITE = "9FA16701-7E6B-4D75-89C4-5C79B25EAC01"
const READ = "9FA16702-7E6B-4D75-89C4-5C79B25EAC01"
const OPEN = 1
const DATA = 2
const CLOSE = 3
const WAIT = 0
const BYTES = 1
const END = 2
const SOCKET_ERROR = 3

function promisify(method, options) {
  return new Promise((resolve, reject) => {
    wx[method]({
      ...options,
      success: resolve,
      fail: (error) => reject(Object.assign(new Error(error.errMsg || "蓝牙操作失败"), error)),
    })
  })
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/** 同一条 GATT 链路同时只跑一个请求，和相框一次只收一个连接一致。 */
class Mutex {
  constructor() {
    this.tail = Promise.resolve()
  }

  run(action) {
    const run = this.tail.then(action, action)
    this.tail = run.then(() => {}, () => {})
    return run
  }
}

class FrameLink {
  /**
   * @param {"wifi"|"bluetooth"} kind
   * @param {{host?: string, port?: number, name?: string, deviceId?: string}} target
   */
  constructor(kind, target) {
    this.kind = kind
    this.host = target.host || ""
    this.port = target.port || 8080
    this.name = target.name || this.host
    this.deviceId = target.deviceId || ""
    this.serviceId = ""
    this.writeId = ""
    this.readId = ""
    this.maxWrite = 20
    this.connected = kind === "wifi"
    this.queue = new Mutex()
    /** 已到达但还没被读取消费的特征值，保持到达顺序。 */
    this.received = []
    /** 正在等下一次读取的 resolver；同一时刻最多一个。 */
    this.waiter = null
    /** onBLECharacteristicValueChange 的回调，非空表示监听已注册。 */
    this.onValue = null
    this.readBusy = false
    this.ignoreEvent = false
    this.closed = false
  }

  base() {
    return `http://${this.host}:${this.port}`
  }

  async close() {
    this.closed = true
    this.stopReader()
    if (this.kind !== "bluetooth" || !this.deviceId) return
    await promisify("closeBLEConnection", { deviceId: this.deviceId }).catch(() => {})
    this.connected = false
  }

  /**
   * @param {"GET"|"POST"|"DELETE"} method
   * @param {string} path
   * @param {{query?: Record<string, string>, body?: ArrayBuffer, headers?: Record<string, string>, timeout?: number}} [options]
   */
  request(method, path, options = {}) {
    const query = Object.entries(options.query || {})
      .filter(([, value]) => value !== undefined && value !== "")
      .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`)
      .join("&")
    const url = `${this.base()}${path}${query ? `?${query}` : ""}`
    if (this.kind === "wifi") return wifiRequest(method, url, options)
    return this.queue.run(() => this.bluetoothRequest(method, url, options))
  }

  async ensureConnected() {
    if (this.connected && this.writeId) return
    await promisify("openBluetoothAdapter", { mode: "central" })
    await promisify("createBLEConnection", { deviceId: this.deviceId, timeout: 15000 })
    const services = await promisify("getBLEDeviceServices", { deviceId: this.deviceId })
    const service = (services.services || []).find((item) => item.uuid.toUpperCase() === SERVICE)
    if (!service) throw new Error("这台设备没有 LumenFolio 服务")
    this.serviceId = service.uuid
    const found = await promisify("getBLEDeviceCharacteristics", {
      deviceId: this.deviceId,
      serviceId: this.serviceId,
    })
    const characteristic = (id) => (found.characteristics || []).find((item) => item.uuid.toUpperCase() === id)
    const write = characteristic(WRITE)
    const read = characteristic(READ)
    if (!write || !read) throw new Error("相框的蓝牙通道不完整")
    this.writeId = write.uuid
    this.readId = read.uuid
    if (wx.setBLEMTU) {
      const negotiated = await promisify("setBLEMTU", { deviceId: this.deviceId, mtu: 247 }).catch(() => null)
      const mtu = negotiated && negotiated.mtu
      if (mtu > 23) this.maxWrite = Math.min(512, mtu - 3)
    }
    this.connected = true
  }

  async bluetoothRequest(method, url, options) {
    await this.ensureConnected()
    // 请求串行执行，开始前还留在队列里的值只可能属于上一次已中断的读取。
    this.received.length = 0
    const body = options.body || new ArrayBuffer(0)
    const header = binaryHeaders({ Host: `${this.host}:${this.port}`, Connection: "close", ...(options.headers || {}) }, body)
    if (body.byteLength) header["Content-Length"] = String(body.byteLength)
    const head = `${method} ${requestPath(url)} HTTP/1.1\r\n${
      Object.entries(header).map(([key, value]) => `${key}: ${value}`).join("\r\n")
    }\r\n\r\n`
    const payload = join(textBytes(head), body)
    await this.write([OPEN])
    const chunk = this.maxWrite - 1
    for (let offset = 0; offset < payload.byteLength; offset += chunk) {
      await this.write([DATA, ...new Uint8Array(payload, offset, Math.min(chunk, payload.byteLength - offset))])
      if (options.onUpload) options.onUpload(Math.min(payload.byteLength, offset + chunk), payload.byteLength)
    }
    const response = await this.readResponse(options.timeout || 20000)
    await this.write([CLOSE]).catch(() => { this.connected = false })
    return response
  }

  write(bytes) {
    return promisify("writeBLECharacteristicValue", {
      deviceId: this.deviceId,
      serviceId: this.serviceId,
      characteristicId: this.writeId,
      value: Uint8Array.from(bytes).buffer,
      writeType: "write",
    })
  }

  async readResponse(timeout) {
    const deadline = Date.now() + timeout
    const chunks = []
    while (Date.now() < deadline) {
      const value = await this.readPacket(deadline - Date.now())
      if (!value) throw new Error("相框没有在时限内返回完整数据")
      const state = value[0]
      if (state === BYTES) chunks.push(value.subarray(1))
      else if (state === WAIT) await delay(20)
      else if (state === END) return decodeHttp(join(...chunks))
      else if (state === SOCKET_ERROR) throw new Error("相框的本地服务没有响应")
      else throw new Error("相框的蓝牙通道中断")
    }
    throw new Error("相框没有在时限内返回完整数据")
  }

  /**
   * 读一次特征。微信把内容交给 onBLECharacteristicValueChange，
   * 成功回调里有时也带着同一包；两者只取先到的那一包。
   * 上一次读还没结束时不再发起新的读，避免并行读把通道打断。
   * @returns {Promise<Uint8Array|null>}
   */
  readPacket(waitMs) {
    if (this.closed) return Promise.reject(new Error("相框连接已关闭"))
    if (this.received.length) return Promise.resolve(this.received.shift())
    this.startReader()
    return new Promise((resolve, reject) => {
      let settled = false
      const finish = (value, error) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        if (this.waiter === finish) this.waiter = null
        if (error) reject(error)
        else resolve(value)
      }
      const timer = setTimeout(() => finish(null), Math.max(1, waitMs))
      this.waiter = finish
      if (this.readBusy) return
      this.readBusy = true
      this.ignoreEvent = false
      promisify("readBLECharacteristicValue", {
        deviceId: this.deviceId,
        serviceId: this.serviceId,
        characteristicId: this.readId,
      }).then((result) => {
        if (!result.value || !result.value.byteLength || settled) return
        this.ignoreEvent = true
        finish(new Uint8Array(result.value))
      }).catch((error) => finish(null, error)).finally(() => {
        this.readBusy = false
      })
    })
  }

  /** 一次会话只注册一次读取事件；回调把值直接交给等待者，没有等待者就排队。 */
  startReader() {
    if (this.onValue) return
    this.onValue = (result) => {
      if (!sameId(result.characteristicId, this.readId) || !result.value || !result.value.byteLength) return
      if (this.ignoreEvent) {
        this.ignoreEvent = false
        return
      }
      const value = new Uint8Array(result.value)
      const waiter = this.waiter
      if (waiter) {
        this.waiter = null
        waiter(value)
      } else {
        this.received.push(value)
      }
    }
    wx.onBLECharacteristicValueChange(this.onValue)
  }

  stopReader() {
    if (this.onValue && wx.offBLECharacteristicValueChange) wx.offBLECharacteristicValueChange(this.onValue)
    this.onValue = null
    this.received.length = 0
    const waiter = this.waiter
    this.waiter = null
    if (waiter) waiter(null)
  }
}

function wifiRequest(method, url, options) {
  return new Promise((resolve, reject) => {
    const task = wx.request({
      url,
      method,
      data: options.body,
      header: binaryHeaders(options.headers, options.body),
      responseType: "arraybuffer",
      timeout: options.timeout || 20000,
      enableHttp2: false,
      success: (result) => resolve({
        status: result.statusCode,
        headers: result.header || {},
        body: result.data instanceof ArrayBuffer ? result.data : textBytes(JSON.stringify(result.data || "")),
      }),
      fail: (error) => reject(new Error(error.errMsg || "无法连接相框")),
    })
    if (options.onUpload && task && task.onProgressUpdate) {
      task.onProgressUpdate((event) => options.onUpload(event.totalBytesSent, event.totalBytesExpectedToSend))
    }
  })
}

function sameId(left, right) {
  return String(left || "").toUpperCase() === String(right || "").toUpperCase()
}

function textBytes(value) {
  const bytes = new Uint8Array(value.length)
  for (let index = 0; index < value.length; index += 1) bytes[index] = value.charCodeAt(index) & 0xff
  return bytes.buffer
}

function join(...parts) {
  const size = parts.reduce((total, part) => total + part.byteLength, 0)
  const out = new Uint8Array(size)
  let offset = 0
  parts.forEach((part) => {
    out.set(new Uint8Array(part), offset)
    offset += part.byteLength
  })
  return out.buffer
}

/**
 * 只发现正在广播相框服务的设备，不列出周围其他蓝牙。
 * 系统设置占住连接后相框不再广播；配对由系统在本次连接时弹出。
 * @param {(devices: {name: string, deviceId: string}[]) => void} onFound
 */
function watchFrames(onFound) {
  const seen = new Map()
  let listening = null
  const deliver = (devices) => {
    let changed = false
    ;(devices || []).forEach((device) => {
      const name = device.name || device.localName || ""
      if (name && !/^LumenFolio/i.test(name)) return
      if (seen.has(device.deviceId)) return
      seen.set(device.deviceId, { name: name || "LumenFolio", deviceId: device.deviceId })
      changed = true
    })
    if (changed) onFound([...seen.values()])
  }
  const ready = (async () => {
    await promisify("openBluetoothAdapter", { mode: "central" })
    for (const service of [SERVICE, SERVICE.toLowerCase()]) {
      const found = await promisify("getConnectedBluetoothDevices", { services: [service] }).catch(() => ({ devices: [] }))
      deliver(found.devices)
    }
    if (seen.size) return
    listening = (result) => deliver(result.devices)
    wx.onBluetoothDeviceFound(listening)
    await promisify("startBluetoothDevicesDiscovery", {
      services: [SERVICE],
      allowDuplicatesKey: false,
      powerLevel: "high",
    })
  })()
  return {
    ready,
    stop() {
      if (listening) wx.offBluetoothDeviceFound(listening)
      return promisify("stopBluetoothDevicesDiscovery", {}).catch(() => {})
    },
  }
}

module.exports = { FrameLink, watchFrames, textBytes }
