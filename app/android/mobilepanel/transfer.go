package mobilepanel

// FrameTransferListener 接收传图阶段和 0 至 100 的总进度估计，由 Android 切回主线程更新界面。
type FrameTransferListener interface {
	OnFrameProgress(stage string, percent int32, message string)
}
