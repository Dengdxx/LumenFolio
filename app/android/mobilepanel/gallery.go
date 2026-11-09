package mobilepanel

import (
	"lumenfolio/gallery"
	"lumenfolio/identity"
)

// DevicePictureList 是一次只读浏览返回的相框图片快照。
type DevicePictureList struct {
	pictures []gallery.Picture
	next     string
}

// Count 返回快照中的图片数。
func (list *DevicePictureList) Count() int32 { return int32(len(list.pictures)) }

// ID 返回用于读取的图片标识，index 须在 Count 范围内。
func (list *DevicePictureList) ID(index int32) string { return list.pictures[index].ID }

// Name 返回图片显示名，index 须在 Count 范围内。
func (list *DevicePictureList) Name(index int32) string { return list.pictures[index].Name }

// Revision 返回缩略图缓存用的图片版本，index 须在 Count 范围内。
func (list *DevicePictureList) Revision(index int32) string { return list.pictures[index].Revision }

// Next 返回下一页的游标，空串表示没有后续页面。
func (list *DevicePictureList) Next() string { return list.next }

// CanDeleteDevicePicture 判断图库条目是否可由用户删除，不依赖连接。
func CanDeleteDevicePicture(source, id string) bool {
	return gallery.CanDelete(gallery.Source(source), id)
}

// StorageInfo 持有一次读取返回的两种存储容量，由 Android 按来源显示。
type StorageInfo struct {
	usage gallery.StorageUsage
}

// Flash 返回 Flash artifacts 区的容量文本。
func (s *StorageInfo) Flash() string { return s.usage.Flash.String() }

// SD 返回 SD 卡的容量文本。
func (s *StorageInfo) SD() string { return s.usage.SD.String() }

// SDAvailable 表示相框已挂载 SD 卡并能读取容量。
func (s *StorageInfo) SDAvailable() bool { return s.usage.SD.TotalBytes != 0 }

// IdentityFromBluetooth 由蓝牙公开地址推出相框身份。推不出时返回空串。
func IdentityFromBluetooth(address string) string {
	return string(identity.FromBluetooth(address))
}
