/** 云开发只用于下载固件。未开通时其余页面仍可用。 */
App({
  onLaunch() {
    if (wx.cloud) wx.cloud.init({ traceUser: false })
  },
})
