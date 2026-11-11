/** 当前小程序只保留一台相框会话。页面切换不重新连接。 */

let session = null

function open(next) {
  if (session && session !== next) session.close()
  session = next
}

function current() {
  return session
}

function close() {
  if (session) session.close()
  session = null
}

module.exports = { open, current, close }
