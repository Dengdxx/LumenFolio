package mobilepanel

import (
	"context"

	"lumenfolio/gallery"
	"lumenfolio/ota"
)

const devicePort = 8080

// DevicePictureList 是一次只读浏览返回的相框图片快照。
type DevicePictureList struct {
	pictures []gallery.Picture
}

// Count 返回快照中的图片数。
func (list *DevicePictureList) Count() int32 { return int32(len(list.pictures)) }

// ID 返回用于读取的图片标识，index 须在 Count 范围内。
func (list *DevicePictureList) ID(index int32) string { return list.pictures[index].ID }

// Name 返回图片显示名，index 须在 Count 范围内。
func (list *DevicePictureList) Name(index int32) string { return list.pictures[index].Name }

// ListDevicePictures 读取相框的 artifacts 或 sd 清单，由 Android 在后台调用。
func ListDevicePictures(host, source string) (*DevicePictureList, error) {
	pictures, err := gallery.List(context.Background(), host, devicePort, gallery.Source(source))
	if err != nil {
		return nil, err
	}
	return &DevicePictureList{pictures: pictures}, nil
}

// PreviewDevicePicture 将指定板端帧还原为 PNG，由 Android 在后台调用。
func PreviewDevicePicture(host, source, id string) ([]byte, error) {
	pixels, err := gallery.Preview(context.Background(), host, devicePort, gallery.Source(source), id)
	if err != nil {
		return nil, err
	}
	return encodePreview(pixels)
}

// ThumbnailDevicePicture 为列表读取一张缩略图 PNG，由 Android 在后台调用。
func ThumbnailDevicePicture(host, source, id string) ([]byte, error) {
	pixels, err := gallery.Thumbnail(context.Background(), host, devicePort, gallery.Source(source), id)
	if err != nil {
		return nil, err
	}
	return encodePreview(pixels)
}

// DisplayDevicePicture 请求显示选中的画面，成功表示设备已安排刷新。
func DisplayDevicePicture(host, source, id string) error {
	return gallery.Display(context.Background(), host, devicePort, gallery.Source(source), id)
}

// StorageInfo 持有一次读取返回的两种存储容量，由 Android 按来源显示。
type StorageInfo struct {
	usage gallery.StorageUsage
}

// Flash 返回 Flash artifacts 区的容量文本。
func (s *StorageInfo) Flash() string { return s.usage.Flash.String() }

// SD 返回 SD 卡的容量文本。
func (s *StorageInfo) SD() string { return s.usage.SD.String() }

// DeviceStorage 读取 Flash 和 SD 卡容量，由 Android 在后台调用。
func DeviceStorage(host string) (*StorageInfo, error) {
	usage, err := gallery.Storage(context.Background(), host, devicePort)
	if err != nil {
		return nil, err
	}
	return &StorageInfo{usage: usage}, nil
}

// DeviceStatus 读取板端电量、充电状态和温湿度，由 Android 在后台调用。
func DeviceStatus(host string) (string, error) {
	return ota.DeviceStatus(context.Background(), host, devicePort)
}
