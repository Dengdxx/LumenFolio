package panel

import (
	"fmt"
	"math"
)

// ColorMetric 选择计算墨水色差的坐标空间。
type ColorMetric uint8

const (
	// MetricSRGB 在 sRGB 编码值上计算加权平方距离。
	MetricSRGB ColorMetric = iota
	// MetricLinearRGB 在线性化的 sRGB 值上计算加权平方距离。
	MetricLinearRGB
)

// ColorOptions 控制所有算法共用的六色选色规则。零值沿用现有设置。
type ColorOptions struct {
	Metric ColorMetric
	// RGBWeights 按 R、G、B 排列，每项取 [0,1]。全零使用 0.3、0.59、0.11。
	RGBWeights [3]float64
}

// NewNearestDither 返回使用指定色差的最近色算法。
func NewNearestDither(options ColorOptions) (Ditherer, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	return pixelDither{name: "最近色", color: options}, nil
}

func (options ColorOptions) validate() error {
	if options.Metric != MetricSRGB && options.Metric != MetricLinearRGB {
		return fmt.Errorf("未知色差算法 %d", options.Metric)
	}
	for _, weight := range options.RGBWeights {
		if !(0 <= weight && weight <= 1) {
			return fmt.Errorf("RGB 权重 %v 超出 [0,1]", weight)
		}
	}
	return nil
}

type rgbPoint [3]float64

type colorMatcher struct {
	channels *[256]float64
	weights  [3]float64
	palette  [6]rgbPoint
}

var encodedChannels = func() (values [256]float64) {
	for i := range values {
		values[i] = float64(i)
	}
	return values
}()

var linearChannels = func() (values [256]float64) {
	for i := range values {
		value := float64(i) / 255
		if value <= 0.04045 {
			values[i] = value / 12.92
		} else {
			values[i] = math.Pow((value+0.055)/1.055, 2.4)
		}
	}
	return values
}()

func (options ColorOptions) matcher() colorMatcher {
	m := colorMatcher{channels: &encodedChannels, weights: options.RGBWeights}
	if options.Metric == MetricLinearRGB {
		m.channels = &linearChannels
	}
	if m.weights == [3]float64{} {
		m.weights = [3]float64{0.3, 0.59, 0.11}
	}
	for i, ink := range inks {
		m.palette[i] = m.point(ink.Preview.R, ink.Preview.G, ink.Preview.B)
	}
	return m
}

func (m *colorMatcher) point(r, g, b byte) rgbPoint {
	return rgbPoint{m.channels[r], m.channels[g], m.channels[b]}
}

func (m *colorMatcher) nearest(r, g, b byte) int {
	point := m.point(r, g, b)
	best, bestDistance := 0, math.MaxFloat64
	for i, ink := range m.palette {
		distance := m.distance(point, ink)
		if distance < bestDistance {
			best, bestDistance = i, distance
		}
	}
	return best
}

func (m *colorMatcher) distance(a, b rgbPoint) float64 {
	dr, dg, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return m.weights[0]*dr*dr + m.weights[1]*dg*dg + m.weights[2]*db*db
}

// nearestTwo 按现有色差比例在最近和次近墨水之间混合，精确命中时保留原色。
func (m *colorMatcher) nearestTwo(r, g, b byte, threshold, strength float64) int {
	point := m.point(r, g, b)
	first, second := 0, 1
	firstDist, secondDist := math.MaxFloat64, math.MaxFloat64
	for i, ink := range m.palette {
		distance := m.distance(point, ink)
		switch {
		case distance < firstDist:
			second, secondDist = first, firstDist
			first, firstDist = i, distance
		case distance < secondDist:
			second, secondDist = i, distance
		}
	}
	if firstDist != 0 && threshold < strength*(firstDist/(firstDist+secondDist)) {
		return second
	}
	return first
}

// bestPair 投影到每对墨水组成的线段，选择混合色与输入色差最小的一对。
// strength 指启用混合的像素比例，剩余像素按最近色映射。
func (m *colorMatcher) bestPair(r, g, b byte, threshold, strength float64) int {
	closest := m.nearest(r, g, b)
	point := m.point(r, g, b)
	bestDistance := m.distance(point, m.palette[closest])
	if threshold >= strength || bestDistance == 0 {
		return closest
	}

	first, second, secondShare := closest, closest, 0.0
	for i, a := range m.palette {
		for j := i + 1; j < len(m.palette); j++ {
			b := m.palette[j]
			var numerator, denominator float64
			for c := range a {
				delta := b[c] - a[c]
				numerator += m.weights[c] * (point[c] - a[c]) * delta
				denominator += m.weights[c] * delta * delta
			}
			// 线段之外的投影退化为单色，已由最近色覆盖。
			if numerator <= 0 || numerator >= denominator {
				continue
			}
			ratio := numerator / denominator
			mixed := rgbPoint{
				a[0] + ratio*(b[0]-a[0]),
				a[1] + ratio*(b[1]-a[1]),
				a[2] + ratio*(b[2]-a[2]),
			}
			if distance := m.distance(point, mixed); distance < bestDistance {
				first, second, secondShare = i, j, ratio
				bestDistance = distance
			}
		}
	}
	if threshold < strength*secondShare {
		return second
	}
	return first
}
