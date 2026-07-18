package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	webpencoder "github.com/deepteams/webp"
	"github.com/disintegration/imaging"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type generatedImage struct {
	Reader    io.Reader
	ObjectKey string
	MIMEType  string
	Size      int64
	Width     int
	Height    int
}

type generatedVariant struct {
	Kind  string
	Image generatedImage
}

func generateThumbnail(source *os.File, sourceMIME, imageID string) (generatedImage, error) {
	decoded, err := decodeOrientedImage(source)
	if err != nil {
		return generatedImage{}, err
	}
	return generateThumbnailFromImage(decoded, sourceMIME, imageID)
}

func decodeOrientedImage(source *os.File) (image.Image, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return imaging.Decode(source, imaging.AutoOrientation(true))
}

func sanitizeImageFile(source *os.File, sourceMIME string) (int64, image.Config, error) {
	decoded, err := decodeOrientedImage(source)
	if err != nil {
		return 0, image.Config{}, err
	}
	if err := source.Truncate(0); err != nil {
		return 0, image.Config{}, err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return 0, image.Config{}, err
	}
	switch sourceMIME {
	case "image/jpeg":
		err = jpeg.Encode(source, decoded, &jpeg.Options{Quality: 95})
	case "image/png":
		err = png.Encode(source, decoded)
	case "image/webp":
		err = webpencoder.Encode(source, decoded, &webpencoder.EncoderOptions{Quality: 95, Method: 4})
	default:
		return 0, image.Config{}, fmt.Errorf("不支持清理 %s 元数据", sourceMIME)
	}
	if err != nil {
		return 0, image.Config{}, err
	}
	size, err := source.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, image.Config{}, err
	}
	bounds := decoded.Bounds()
	return size, image.Config{Width: bounds.Dx(), Height: bounds.Dy()}, nil
}

func generateThumbnailFromImage(decoded image.Image, sourceMIME, imageID string) (generatedImage, error) {
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return generatedImage{}, fmt.Errorf("图片尺寸无效")
	}
	const longest = 480
	targetWidth, targetHeight := width, height
	if width > longest || height > longest {
		if width >= height {
			targetWidth = longest
			targetHeight = max(1, height*longest/width)
		} else {
			targetHeight = longest
			targetWidth = max(1, width*longest/height)
		}
	}
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	xdraw.CatmullRom.Scale(target, target.Bounds(), decoded, bounds, xdraw.Over, nil)
	var buffer bytes.Buffer
	mimeType, extension := "image/png", ".png"
	var err error
	if sourceMIME == "image/jpeg" {
		mimeType, extension = "image/jpeg", ".jpg"
		err = jpeg.Encode(&buffer, target, &jpeg.Options{Quality: 82})
	} else {
		err = png.Encode(&buffer, target)
	}
	if err != nil {
		return generatedImage{}, err
	}
	return generatedImage{
		Reader: bytes.NewReader(buffer.Bytes()), ObjectKey: "variants/" + imageID + "/thumbnail" + extension,
		MIMEType: mimeType, Size: int64(buffer.Len()), Width: targetWidth, Height: targetHeight,
	}, nil
}

func generateProcessingVariants(source *os.File, sourceMIME, imageID string, settings ProcessingSettings) ([]generatedVariant, []error) {
	if sourceMIME == "image/gif" || (!settings.GenerateWebP && !settings.WatermarkEnabled) {
		return nil, nil
	}
	decoded, err := decodeOrientedImage(source)
	if err != nil {
		return nil, []error{fmt.Errorf("解码派生处理源图失败: %w", err)}
	}
	return generateProcessingVariantsFromImage(decoded, sourceMIME, imageID, settings)
}

func generateProcessingVariantsFromImage(decoded image.Image, sourceMIME, imageID string, settings ProcessingSettings) ([]generatedVariant, []error) {
	if sourceMIME == "image/gif" || (!settings.GenerateWebP && !settings.WatermarkEnabled) {
		return nil, nil
	}
	var generated []generatedVariant
	var failures []error
	if settings.GenerateWebP && sourceMIME != "image/webp" {
		webp, err := generateWebPVariant(decoded, imageID, settings.WebPQuality)
		if err != nil {
			failures = append(failures, fmt.Errorf("生成 WebP 失败: %w", err))
		} else {
			generated = append(generated, generatedVariant{Kind: "webp", Image: webp})
		}
	}
	if settings.WatermarkEnabled {
		watermarked, err := generateWatermarkedVariant(decoded, sourceMIME, imageID, settings.WatermarkText, settings.WatermarkPosition)
		if err != nil {
			failures = append(failures, fmt.Errorf("生成水印图失败: %w", err))
		} else {
			generated = append(generated, generatedVariant{Kind: "watermarked", Image: watermarked})
		}
	}
	return generated, failures
}

func generateWebPVariant(source image.Image, imageID string, quality int) (generatedImage, error) {
	if source == nil || source.Bounds().Dx() <= 0 || source.Bounds().Dy() <= 0 {
		return generatedImage{}, errors.New("图片尺寸无效")
	}
	var buffer bytes.Buffer
	if err := webpencoder.Encode(&buffer, source, &webpencoder.EncoderOptions{
		Quality: float32(quality), Method: 4,
	}); err != nil {
		return generatedImage{}, err
	}
	return generatedImage{
		Reader: bytes.NewReader(buffer.Bytes()), ObjectKey: "variants/" + imageID + "/image.webp",
		MIMEType: "image/webp", Size: int64(buffer.Len()),
		Width: source.Bounds().Dx(), Height: source.Bounds().Dy(),
	}, nil
}

func generateWatermarkedVariant(source image.Image, sourceMIME, imageID, text, position string) (generatedImage, error) {
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return generatedImage{}, fmt.Errorf("图片尺寸无效")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), source, bounds.Min, draw.Src)
	face := basicfont.Face7x13
	textWidth := font.MeasureString(face, text).Ceil()
	textHeight := face.Metrics().Height.Ceil()
	const padding = 12
	x, baseline := watermarkCoordinates(canvas.Bounds(), textWidth, textHeight, face.Metrics().Ascent.Ceil(), padding, position)

	shadow := font.Drawer{
		Dst: canvas, Src: image.NewUniform(color.RGBA{A: 190}), Face: face,
		Dot: fixed.P(x+1, baseline+1),
	}
	shadow.DrawString(text)
	foreground := font.Drawer{
		Dst: canvas, Src: image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 230}), Face: face,
		Dot: fixed.P(x, baseline),
	}
	foreground.DrawString(text)

	var buffer bytes.Buffer
	mimeType, extension := "image/png", ".png"
	var err error
	if sourceMIME == "image/jpeg" {
		mimeType, extension = "image/jpeg", ".jpg"
		err = jpeg.Encode(&buffer, canvas, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buffer, canvas)
	}
	if err != nil {
		return generatedImage{}, err
	}
	return generatedImage{
		Reader: bytes.NewReader(buffer.Bytes()), ObjectKey: "variants/" + imageID + "/watermarked" + extension,
		MIMEType: mimeType, Size: int64(buffer.Len()), Width: bounds.Dx(), Height: bounds.Dy(),
	}, nil
}

func watermarkCoordinates(bounds image.Rectangle, textWidth, textHeight, ascent, padding int, position string) (int, int) {
	left := min(padding, max(0, bounds.Dx()-textWidth))
	right := max(0, bounds.Dx()-textWidth-padding)
	top := min(bounds.Dy(), padding+ascent)
	bottom := max(ascent, bounds.Dy()-padding-(textHeight-ascent))
	centerX := max(0, (bounds.Dx()-textWidth)/2)
	centerY := max(ascent, (bounds.Dy()-textHeight)/2+ascent)
	switch position {
	case "top-left":
		return left, top
	case "top-right":
		return right, top
	case "bottom-left":
		return left, bottom
	case "center":
		return centerX, centerY
	default:
		return right, bottom
	}
}
