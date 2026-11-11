const assert = require("node:assert/strict")
const { requestPath, binaryHeaders, decodeText, decodeHttp } = require("../miniprogram/frame/http")
const { frameFileName } = require("../miniprogram/frame/protocol")

assert.equal(requestPath("http://127.0.0.1:8080/frames?source=sd"), "/frames?source=sd")

const headers = binaryHeaders({}, new ArrayBuffer(4))
assert.equal(headers["Content-Type"], "application/octet-stream")
assert.equal(binaryHeaders({ "Content-Type": "text/plain" }, new ArrayBuffer(4))["Content-Type"], "text/plain")

const payload = Buffer.from('{"name":"照片"}', "utf8")
assert.equal(JSON.parse(decodeText(payload)).name, "照片")

const chunked = Buffer.concat([
  Buffer.from("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n", "ascii"),
  Buffer.from(payload.length.toString(16) + "\r\n"),
  payload,
  Buffer.from("\r\n0\r\n\r\n"),
])
const response = decodeHttp(chunked)
assert.equal(response.status, 200)
assert.equal(JSON.parse(decodeText(response.body)).name, "照片")

const longName = "三十个汉字".repeat(3)
const file = frameFileName(`${longName}.jpg`, 1700000000000)
assert.ok(Buffer.byteLength(file) < 96, file)
assert.ok(file.endsWith("-1700000000000.bin"))

console.log("http ok")
