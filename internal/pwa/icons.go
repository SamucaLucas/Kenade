package pwa

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// EnsureIcons gera automaticamente os ícones PNG de alta resolução (180x180 para iOS, 192x192 e 512x512 para Android PWA)
// utilizando a paleta oficial do Kenade caso ainda não existam na pasta web/.
func EnsureIcons() {
	targets := []struct {
		path string
		size int
	}{
		{"./web/icon-180.png", 180},
		{"./web/icon-192.png", 192},
		{"./web/icon-512.png", 512},
	}

	for _, t := range targets {
		if _, err := os.Stat(t.path); err == nil {
			continue
		}
		img := renderKenadeIcon(t.size)
		f, err := os.Create(t.path)
		if err != nil {
			continue
		}
		_ = png.Encode(f, img)
		_ = f.Close()
	}
}

func renderKenadeIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	// Cores oficiais Kenade
	bgTop := color.RGBA{30, 27, 75, 255}     // #1E1B4B Índigo Profundo
	bgBottom := color.RGBA{15, 13, 38, 255}  // #0F0D26
	cBlue := color.RGBA{37, 99, 235, 255}    // #2563EB Opção A
	cPurple := color.RGBA{124, 58, 237, 255} // #7C3AED Opção B
	cCyan := color.RGBA{8, 145, 178, 255}    // #0891B2 Opção C
	cMagenta := color.RGBA{219, 39, 119, 255}// #DB2777 Opção D
	cAmber := color.RGBA{245, 158, 11, 255}  // #F59E0B Âmbar Dourado
	cWhite := color.RGBA{248, 250, 252, 255} // #F8FAFC Branco Gelo

	for y := 0; y < size; y++ {
		t := float64(y) / s
		bgR := uint8(float64(bgTop.R)*(1-t) + float64(bgBottom.R)*t)
		bgG := uint8(float64(bgTop.G)*(1-t) + float64(bgBottom.G)*t)
		bgB := uint8(float64(bgTop.B)*(1-t) + float64(bgBottom.B)*t)

		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, color.RGBA{bgR, bgG, bgB, 255})
		}
	}

	// Quadrantes translúcidos das 4 cores do Quiz
	drawRoundedRect(img, s*0.125, s*0.125, s*0.33, s*0.33, s*0.09, cBlue, 0.30)
	drawRoundedRect(img, s*0.545, s*0.125, s*0.33, s*0.33, s*0.09, cPurple, 0.30)
	drawRoundedRect(img, s*0.125, s*0.545, s*0.33, s*0.33, s*0.09, cCyan, 0.30)
	drawRoundedRect(img, s*0.545, s*0.545, s*0.33, s*0.33, s*0.09, cMagenta, 0.30)

	// Haste vertical esquerda do "K" em Branco Gelo
	drawRoundedRect(img, s*0.26, s*0.20, s*0.15, s*0.60, s*0.04, cWhite, 1.0)

	// Pernas diagonais do "K" em Âmbar Dourado (#F59E0B)
	drawThickLine(img, s*0.39, s*0.50, s*0.70, s*0.23, s*0.075, cAmber)
	drawThickLine(img, s*0.42, s*0.47, s*0.71, s*0.77, s*0.075, cAmber)

	return img
}

func drawRoundedRect(img *image.RGBA, rx, ry, rw, rh, radius float64, col color.RGBA, alpha float64) {
	minX := int(math.Max(0, rx))
	maxX := int(math.Min(float64(img.Bounds().Dx()-1), rx+rw))
	minY := int(math.Max(0, ry))
	maxY := int(math.Min(float64(img.Bounds().Dy()-1), ry+rh))

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			fx, fy := float64(x), float64(y)
			inside := true
			var cx, cy float64
			checkCorner := false

			if fx < rx+radius && fy < ry+radius {
				cx, cy = rx+radius, ry+radius
				checkCorner = true
			} else if fx > rx+rw-radius && fy < ry+radius {
				cx, cy = rx+rw-radius, ry+radius
				checkCorner = true
			} else if fx < rx+radius && fy > ry+rh-radius {
				cx, cy = rx+radius, ry+rh-radius
				checkCorner = true
			} else if fx > rx+rw-radius && fy > ry+rh-radius {
				cx, cy = rx+rw-radius, ry+rh-radius
				checkCorner = true
			}

			if checkCorner {
				dist := math.Hypot(fx-cx, fy-cy)
				if dist > radius {
					inside = false
				}
			}

			if inside {
				blendPixel(img, x, y, col, alpha)
			}
		}
	}
}

func drawThickLine(img *image.RGBA, x1, y1, x2, y2, radius float64, col color.RGBA) {
	minX := int(math.Max(0, math.Min(x1, x2)-radius-2))
	maxX := int(math.Min(float64(img.Bounds().Dx()-1), math.Max(x1, x2)+radius+2))
	minY := int(math.Max(0, math.Min(y1, y2)-radius-2))
	maxY := int(math.Min(float64(img.Bounds().Dy()-1), math.Max(y1, y2)+radius+2))

	dx := x2 - x1
	dy := y2 - y1
	lenSq := dx*dx + dy*dy

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x), float64(y)
			t := 0.0
			if lenSq > 0 {
				t = ((px-x1)*dx + (py-y1)*dy) / lenSq
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
			}
			projX := x1 + t*dx
			projY := y1 + t*dy
			dist := math.Hypot(px-projX, py-projY)
			if dist <= radius {
				blendPixel(img, x, y, col, 1.0)
			}
		}
	}
}

func blendPixel(img *image.RGBA, x, y int, col color.RGBA, alpha float64) {
	if alpha >= 1.0 {
		img.SetRGBA(x, y, col)
		return
	}
	dst := img.RGBAAt(x, y)
	r := uint8(float64(dst.R)*(1-alpha) + float64(col.R)*alpha)
	g := uint8(float64(dst.G)*(1-alpha) + float64(col.G)*alpha)
	b := uint8(float64(dst.B)*(1-alpha) + float64(col.B)*alpha)
	img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
}
