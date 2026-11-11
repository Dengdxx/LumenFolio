/** 相框 HTTP 报文的编解。不依赖微信运行时没有的 URL，也不把 UTF-8 当单字节。 */

function requestPath(url) {
  const scheme = url.indexOf("://")
  const start = url.indexOf("/", scheme < 0 ? 0 : scheme + 3)
  return start < 0 ? "/" : url.slice(start)
}

function binaryHeaders(headers, body) {
  const next = { ...(headers || {}) }
  if (body && body.byteLength && !next["Content-Type"] && !next["content-type"]) {
    next["Content-Type"] = "application/octet-stream"
  }
  return next
}

function decodeText(buffer) {
  const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer)
  let text = ""
  for (let index = 0; index < bytes.length;) {
    const first = bytes[index]
    if (first < 0x80) {
      text += String.fromCharCode(first)
      index += 1
    } else if (first < 0xe0 && index + 1 < bytes.length) {
      text += String.fromCharCode(((first & 0x1f) << 6) | (bytes[index + 1] & 0x3f))
      index += 2
    } else if (first < 0xf0 && index + 2 < bytes.length) {
      text += String.fromCharCode(((first & 0x0f) << 12) | ((bytes[index + 1] & 0x3f) << 6) | (bytes[index + 2] & 0x3f))
      index += 3
    } else if (index + 3 < bytes.length) {
      const code = ((first & 0x07) << 18) | ((bytes[index + 1] & 0x3f) << 12) | ((bytes[index + 2] & 0x3f) << 6) | (bytes[index + 3] & 0x3f)
      const shifted = code - 0x10000
      text += String.fromCharCode(0xd800 + (shifted >> 10), 0xdc00 + (shifted & 0x3ff))
      index += 4
    } else {
      text += "�"
      index += 1
    }
  }
  return text
}

function decodeHttp(buffer) {
  const bytes = new Uint8Array(buffer)
  const separator = find(bytes, ascii("\r\n\r\n"))
  if (separator < 0) throw new Error("相框返回了不完整的响应")
  const head = decodeText(bytes.subarray(0, separator)).split("\r\n")
  const status = Number((head[0].match(/\s(\d{3})\s/) || [])[1])
  const headers = {}
  head.slice(1).forEach((line) => {
    const index = line.indexOf(":")
    if (index > 0) headers[line.slice(0, index).toLowerCase()] = line.slice(index + 1).trim()
  })
  let body = bytes.subarray(separator + 4)
  if ((headers["transfer-encoding"] || "").toLowerCase().includes("chunked")) body = unchunk(body)
  return { status, headers, body: body.buffer.slice(body.byteOffset, body.byteOffset + body.byteLength) }
}

function unchunk(bytes) {
  const parts = []
  let index = 0
  while (index < bytes.length) {
    let line = index
    while (line + 1 < bytes.length && !(bytes[line] === 13 && bytes[line + 1] === 10)) line += 1
    if (line + 1 >= bytes.length) throw new Error("相框返回了不完整的分块")
    const size = Number.parseInt(decodeText(bytes.subarray(index, line)).split(";")[0], 16)
    if (!Number.isFinite(size)) throw new Error("相框返回了无法识别的分块")
    index = line + 2
    if (size === 0) break
    parts.push(bytes.subarray(index, index + size))
    index += size + 2
  }
  const length = parts.reduce((total, part) => total + part.length, 0)
  const body = new Uint8Array(length)
  let offset = 0
  parts.forEach((part) => {
    body.set(part, offset)
    offset += part.length
  })
  return body
}

function ascii(value) {
  const bytes = new Uint8Array(value.length)
  for (let index = 0; index < value.length; index += 1) bytes[index] = value.charCodeAt(index) & 0xff
  return bytes
}

function find(bytes, needle) {
  for (let index = 0; index <= bytes.length - needle.length; index += 1) {
    if (needle.every((value, offset) => bytes[index + offset] === value)) return index
  }
  return -1
}

module.exports = { requestPath, binaryHeaders, decodeText, decodeHttp }
