package app

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"

	xdraw "golang.org/x/image/draw"
)

type generatedImage struct {
	Reader    io.Reader
	ObjectKey string
	MIMEType  string
	Size      int64
	Width     int
	Height    int
}

func generateThumbnail(source *os.File, sourceMIME, imageID string) (generatedImage, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return generatedImage{}, err
	}
	decoded, _, err := image.Decode(source)
	if err != nil {
		return generatedImage{}, err
	}
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
