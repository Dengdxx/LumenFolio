package gallery

import (
	"container/list"
	"context"
	"image"
	"sync"
)

const thumbnailCacheSize = 64

type thumbnailKey struct {
	host     string
	port     int
	source   Source
	id       string
	revision string
}

type cachedThumbnail struct {
	key   thumbnailKey
	image *image.NRGBA
}

// thumbs 只持有进程内近期使用的小图，网络请求在锁外进行。
var thumbs = struct {
	sync.Mutex
	order list.List
	items map[thumbnailKey]*list.Element
}{items: make(map[thumbnailKey]*list.Element)}

// ThumbnailFor 复用相框、来源和图片版本一致的缩略图。调用方将返回图像视为只读快照。
func ThumbnailFor(ctx context.Context, host string, port int, source Source, picture Picture) (*image.NRGBA, error) {
	if picture.Revision == "" {
		return Thumbnail(ctx, host, port, source, picture.ID)
	}
	key := thumbnailKey{host: host, port: port, source: source, id: picture.ID, revision: picture.Revision}
	thumbs.Lock()
	if item := thumbs.items[key]; item != nil {
		thumbs.order.MoveToFront(item)
		img := item.Value.(cachedThumbnail).image
		thumbs.Unlock()
		return img, nil
	}
	thumbs.Unlock()

	img, err := Thumbnail(ctx, host, port, source, picture.ID)
	if err != nil {
		return nil, err
	}
	thumbs.Lock()
	defer thumbs.Unlock()
	if item := thumbs.items[key]; item != nil {
		thumbs.order.MoveToFront(item)
		return item.Value.(cachedThumbnail).image, nil
	}
	thumbs.items[key] = thumbs.order.PushFront(cachedThumbnail{key: key, image: img})
	if thumbs.order.Len() > thumbnailCacheSize {
		old := thumbs.order.Back()
		delete(thumbs.items, old.Value.(cachedThumbnail).key)
		thumbs.order.Remove(old)
	}
	return img, nil
}
