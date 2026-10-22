package app

// preview.go — 水印预览：低分辨率 + 对角线 "0brand" 文字水印

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"os"

	"github.com/disintegration/imaging"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const previewWidth = 400

var watermarkFont *opentype.Font

func init() {
	for _, p := range []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
	} {
		if b, err := os.ReadFile(p); err == nil {
			if f, err := opentype.Parse(b); err == nil {
				watermarkFont = f
				return
			}
		}
	}
}

// CreatePreview 从原图生成带水印的低分辨率预览 JPEG
func CreatePreview(fullpath, previewPath string) error {
	f, err := os.Open(fullpath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	previewH := previewWidth * src.Bounds().Dy() / src.Bounds().Dx()
	dst := imaging.Resize(src, previewWidth, previewH, imaging.Lanczos)
	canvas := image.NewRGBA(image.Rect(0, 0, previewWidth, previewH))
	draw.Draw(canvas, canvas.Bounds(), dst, image.Point{}, draw.Src)
	drawWatermark(canvas, previewWidth, previewH)
	out, err := os.Create(previewPath)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()
	return jpeg.Encode(out, canvas, &jpeg.Options{Quality: 78})
}

func drawWatermark(canvas *image.RGBA, w, h int) {
	if watermarkFont != nil {
		drawTextWatermark(canvas, w, h)
	} else {
		drawStripeWatermark(canvas, w, h)
	}
}

// drawTextWatermark 居中大字 "0brand"（半透明 + 阴影）
func drawTextWatermark(canvas *image.RGBA, w, h int) {
	const text = "M-brand"
	size := float64(w) / 7.0
	face, err := opentype.NewFace(watermarkFont, &opentype.FaceOptions{Size: size, DPI: 72})
	if err != nil {
		drawStripeWatermark(canvas, w, h)
		return
	}
	defer face.Close()
	b, _ := font.BoundString(face, text)
	tw, th := b.Max.X-b.Min.X, b.Max.Y-b.Min.Y
	dot := fixed.Point26_6{
		X: (fixed.I(w)-tw)/2 - b.Min.X,
		Y: (fixed.I(h)-th)/2 - b.Min.Y + th,
	}
	// 阴影
	d := &font.Drawer{Dst: canvas, Src: image.NewUniform(color.RGBA{A: 50}), Face: face,
		Dot: fixed.Point26_6{X: dot.X + fixed.I(2), Y: dot.Y + fixed.I(2)}}
	d.DrawString(text)
	// 主文字
	d = &font.Drawer{Dst: canvas, Src: image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 85}), Face: face, Dot: dot}
	d.DrawString(text)
}

// drawStripeWatermark 无字体时的备用：半透明暗化 + 对角线条纹
func drawStripeWatermark(canvas *image.RGBA, w, h int) {
	draw.Draw(canvas, canvas.Bounds(),
		image.NewUniform(color.RGBA{A: 25}), image.Point{}, draw.Over)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			if (x+y)%40 < 3 {
				canvas.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 15})
			}
		}
	}
}

// EnsurePreview 确保预览文件存在
func EnsurePreview(fullpath string) string {
	base := fullpath
	if len(base) > 4 && base[len(base)-4:] == ".png" {
		base = base[:len(base)-4]
	}
	previewPath := base + "_preview.jpg"
	if _, err := os.Stat(previewPath); os.IsNotExist(err) {
		if err := CreatePreview(fullpath, previewPath); err != nil {
			return fullpath
		}
	}
	return previewPath
}
