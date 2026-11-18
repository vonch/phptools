package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// Mark draws the PHPTools mark at the requested pixel size.
// Windows file icons, the window, and the tray all use this drawing.
func Mark(size int) *image.RGBA {
	if size < 16 {
		size = 16
	}
	im := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{19, 29, 47, 255}
	ink := color.RGBA{112, 231, 190, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			im.SetRGBA(x, y, bg)
		}
	}
	// Boxes are specified on a 32px grid and scaled to the requested size.
	for _, box := range [][4]int{{8, 7, 13, 26}, {12, 7, 24, 12}, {20, 11, 25, 18}, {12, 16, 24, 21}} {
		x0 := box[0] * size / 32
		y0 := box[1] * size / 32
		x1 := box[2] * size / 32
		y1 := box[3] * size / 32
		if x1 <= x0 {
			x1 = x0 + 1
		}
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for y := y0; y < y1 && y < size; y++ {
			for x := x0; x < x1 && x < size; x++ {
				im.SetRGBA(x, y, ink)
			}
		}
	}
	return im
}

// Sizes are the icon sizes Windows Explorer and shortcuts actually use.
func Sizes() []int {
	return []int{16, 32, 48, 64, 128, 256}
}

func Images() []image.Image {
	sizes := Sizes()
	images := make([]image.Image, 0, len(sizes))
	for _, size := range sizes {
		images = append(images, Mark(size))
	}
	return images
}

func PNG() []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Mark(32)); err != nil {
		return nil
	}
	return buf.Bytes()
}
