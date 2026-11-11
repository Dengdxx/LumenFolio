/** 相框身份、容量和固件镜像的展示规则。传输细节留在 link.js。 */

const MAC = /^[0-9a-f]{2}(?::[0-9a-f]{2}){5}$/

/** 把冒号分隔的 MAC 统一成小写。格式不对返回空串。 */
function parseMac(text) {
  const parts = String(text || "").trim().toLowerCase().split(":")
  if (parts.length !== 6 || parts.some((part) => !/^[0-9a-f]{2}$/.test(part))) return ""
  return parts.join(":")
}

/**
 * 由蓝牙公开地址推出基 MAC。
 * 当前固件的蓝牙地址是基 MAC 加 2，进位只发生在最低字节。
 * @param {string} address
 * @returns {string}
 */
function identityFromBluetooth(address) {
  const mac = parseMac(address)
  if (!mac) return ""
  const bytes = mac.split(":").map((part) => Number.parseInt(part, 16))
  if ((bytes[0] & 1) !== 0 || bytes[5] < 2) return ""
  bytes[5] -= 2
  return bytes.map((value) => value.toString(16).padStart(2, "0")).join(":")
}

function formatBytes(value) {
  const bytes = Number(value) || 0
  if (bytes < 1000) return `${bytes} B`
  if (bytes < 1000 * 1000) return `${(bytes / 1000).toFixed(bytes < 10000 ? 1 : 0)} KB`
  return `${(bytes / 1000000).toFixed(bytes < 10000000 ? 1 : 0)} MB`
}

/** @param {{used_bytes?: number, total_bytes?: number}} usage */
function formatUsage(usage) {
  const total = Number(usage && usage.total_bytes) || 0
  if (!total) return "容量不可用"
  const used = Number(usage.used_bytes) || 0
  const percent = (100 * used / total).toFixed(1)
  return `${formatBytes(used)}/${formatBytes(total)}（${percent}%）`
}

const CHARGING = { charging: "充电中", full: "已充满", idle: "未充电", unknown: "充电状态未知" }

/** 把 /ota/status 里的运行状态收成一行。缺字段的旧固件返回空串。 */
function formatStatus(status) {
  if (!status) return ""
  const parts = []
  if (status.battery_percent != null) parts.push(`电量 ${status.battery_percent}%`)
  else if (status.battery_mv != null) parts.push(`电池 ${status.battery_mv} mV`)
  if (CHARGING[status.charge]) parts.push(CHARGING[status.charge])
  if (status.temperature_c != null) parts.push(`${Number(status.temperature_c).toFixed(1)} °C`)
  if (status.humidity_pct != null) parts.push(`${Number(status.humidity_pct).toFixed(0)}%`)
  return parts.join(" · ")
}

/**
 * 只认普通 ESP-IDF 应用镜像。完整校验由相框完成，这里只拦明显选错的文件。
 * @param {ArrayBuffer} buffer
 */
function inspectFirmware(buffer) {
  const bytes = new Uint8Array(buffer)
  if (bytes.length < 288 || bytes[0] !== 0xe9) throw new Error("不是 ESP-IDF 应用镜像，请选择 LumenFolio.bin")
  const view = new DataView(buffer)
  if (view.getUint32(32, true) !== 0xabcd5432) throw new Error("不是 ESP-IDF 应用镜像，请选择 LumenFolio.bin")
  const text = (start, length) => {
    const end = bytes.indexOf(0, start)
    const stop = end < 0 || end > start + length ? start + length : end
    return String.fromCharCode(...bytes.subarray(start, stop))
  }
  const version = text(48, 32)
  const project = text(80, 32)
  if (project !== "LumenFolio" || !version || /[\u0000-\u001f\u007f]/.test(version)) {
    throw new Error("镜像必须属于 LumenFolio，且包含可显示的版本")
  }
  return { version, project, bytes: buffer.byteLength }
}

/** SD 文件名按固件的字节上限截断，不能按字符数截。 */
function frameFileName(name, now = Date.now()) {
  const base = String(name || "").replace(/\\/g, "/").split("/").pop().replace(/\.[^.]*$/, "")
  const suffix = `-${now}.bin`
  let cleaned = ""
  for (const char of Array.from(base)) {
    const next = /^\p{L}$/u.test(char) || /^\p{N}$/u.test(char) || " _-".includes(char) ? char : "_"
    if (utf8Length(cleaned + next + suffix) >= 96) break
    cleaned += next
  }
  cleaned = cleaned.trim() || "photo"
  return `${cleaned}${suffix}`
}

function utf8Length(value) {
  return Array.from(value).reduce((total, char) => {
    const code = char.codePointAt(0)
    if (code < 0x80) return total + 1
    if (code < 0x800) return total + 2
    if (code < 0x10000) return total + 3
    return total + 4
  }, 0)
}

module.exports = {
  MAC,
  parseMac,
  identityFromBluetooth,
  formatBytes,
  formatUsage,
  formatStatus,
  inspectFirmware,
  frameFileName,
}
