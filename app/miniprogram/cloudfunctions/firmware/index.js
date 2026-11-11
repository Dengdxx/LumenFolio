const cloud = require("wx-server-sdk")

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV })

const INDEX = "firmware/index.json"

/**
 * 列出可下载的应用固件。
 *
 * 云存储没有按目录列举的开放接口。发布固件时把清单写成 firmware/index.json，
 * 每项含 name、fileID、size、updated。小程序只拿到这份清单，不遍历存储桶。
 */
exports.main = async (event) => {
  if (!event || event.action !== "list") return { files: [] }
  const fileID = process.env.LUMENFOLIO_FIRMWARE_INDEX
  if (!fileID) return { files: [], configured: false }
  const downloaded = await cloud.downloadFile({ fileID })
  const parsed = JSON.parse(downloaded.fileContent.toString("utf8"))
  const files = Array.isArray(parsed) ? parsed : []
  return {
    configured: true,
    index: INDEX,
    files: files
      .filter((file) => file && typeof file.name === "string" && typeof file.fileID === "string" && file.name.endsWith(".bin"))
      .map((file) => ({
        name: file.name,
        fileID: file.fileID,
        size: Number(file.size) || 0,
        updated: file.updated || "",
      })),
  }
}
