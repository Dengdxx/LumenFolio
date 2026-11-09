package desktop

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestOTAPageCenteredWithWindowControls(t *testing.T) {
	u := newTestUI()
	u.ota.open = true
	var router input.Router
	var ops op.Ops
	size := image.Pt(1180, 760)
	u.layout(layout.Context{Ops: &ops, Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(size)})
	router.Frame(&ops)
	var titleX int
	var found bool
	var visit func([]input.SemanticNode)
	visit = func(nodes []input.SemanticNode) {
		for _, node := range nodes {
			if node.Desc.Label == "固件升级" {
				titleX, found = node.Desc.Bounds.Min.X, true
			}
			visit(node.Children)
		}
	}
	visit(router.AppendSemantics(nil))
	if !found || titleX < 300 || titleX > 400 {
		t.Fatalf("升级页标题应位于居中卡片中，找到 %v，横坐标 %d", found, titleX)
	}
}
