/**
 * 云开发只保存可下载的应用固件。
 * 环境 ID 留空时使用工具当前选定的环境；未开通时页面保持可用，只是不能拉固件。
 */

const FOLDER = "firmware/"

function cloudReady() {
  return typeof wx.cloud !== "undefined" && wx.cloud
}

function ensureCloud() {
  if (!cloudReady()) throw new Error("当前基础库没有云开发能力")
  if (!ensureCloud.ready) {
    wx.cloud.init({ traceUser: false })
    ensureCloud.ready = true
  }
}

/** 读取云函数返回的固件清单。云函数未部署或环境未开通时由页面显示原因。 */
async function listFirmware() {
  ensureCloud()
  const listed = await wx.cloud.callFunction({ name: "firmware", data: { action: "list" } })
  const result = listed.result || {}
  if (!Array.isArray(result.files)) throw new Error("云端没有返回固件列表")
  return result
}

/** 把选定固件下载到本地临时文件，返回临时路径和文件内容。 */
async function downloadFirmware(fileID) {
  ensureCloud()
  const result = await wx.cloud.downloadFile({ fileID })
  const file = await new Promise((resolve, reject) => {
    wx.getFileSystemManager().readFile({
      filePath: result.tempFilePath,
      success: resolve,
      fail: () => reject(new Error("已下载的固件无法读取")),
    })
  })
  return { path: result.tempFilePath, data: file.data }
}

module.exports = { FOLDER, cloudReady, listFirmware, downloadFirmware }
