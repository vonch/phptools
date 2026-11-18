package icon

import "testing"

func TestMarkDrawsTheInk(t *testing.T) {
	for _, size := range []int{16, 32, 256} {
		im := Mark(size)
		if im.Bounds().Dx() != size || im.Bounds().Dy() != size {
			t.Fatalf("size %d: got %v", size, im.Bounds())
		}
		bg := im.RGBAAt(0, 0)
		if bg.R != 19 || bg.G != 29 || bg.B != 47 {
			t.Fatalf("size %d: background is %v", size, bg)
		}
		ink := 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				pixel := im.RGBAAt(x, y)
				if pixel.R == 112 && pixel.G == 231 && pixel.B == 190 {
					ink++
				}
			}
		}
		if ink == 0 {
			t.Fatalf("size %d: icon has no mark", size)
		}
	}
}

func TestPNGIsReadable(t *testing.T) {
	data := PNG()
	if len(data) < 32 {
		t.Fatal("png is empty")
	}
}
