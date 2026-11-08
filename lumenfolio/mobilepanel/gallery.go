package mobilepanel

import (
	"context"

	"lumenfolio/gallery"
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
