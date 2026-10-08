package process

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
)

// Photos are wrapped in a one-page PDF so every document views, shares and
// OCRs the same way. JPEGs go in as they are (no re-encoding); the EXIF
// orientation is applied by the page's transform instead of by rotating
// pixels, which would need the whole image in memory.

// pageLong is the long side of the page in points (A4).
const pageLong = 842.0

// jpegToPDF writes a PDF showing the JPEG at path.
func jpegToPDF(path, out string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not a readable JPEG: %w", err)
	}
	colorSpace := "/DeviceRGB"
	switch cfg.ColorModel {
	case color.GrayModel:
		colorSpace = "/DeviceGray"
	case color.CMYKModel:
		colorSpace = "/DeviceCMYK /Decode [1 0 1 0 1 0 1 0]" // Adobe JPEGs store CMYK inverted
	}
	return writeImagePDF(out, data, cfg.Width, cfg.Height, colorSpace, exifOrientation(data))
}

// pngToPDF re-encodes a PNG as JPEG (over white, for transparency) and
// wraps it. The original PNG is kept as the document's file.
func pngToPDF(path, out, tmp string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	img, err := png.Decode(bufio.NewReader(f))
	f.Close()
	if err != nil {
		return fmt.Errorf("not a readable PNG: %w", err)
	}
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
	j, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(j, flat, &jpeg.Options{Quality: 90}); err != nil {
		j.Close()
		return err
	}
	if err := j.Close(); err != nil {
		return err
	}
	return jpegToPDF(tmp, out)
}

// writeImagePDF writes a one-page PDF with a DCT-encoded image, turned or
// mirrored as the EXIF orientation says.
func writeImagePDF(out string, jpg []byte, w, h int, colorSpace string, orientation int) error {
	if orientation < 1 || orientation > 8 {
		orientation = 1
	}
	dw, dh := w, h // displayed size in pixels
	if orientation >= 5 {
		dw, dh = h, w
	}
	scale := pageLong / float64(max(w, h))
	pw, ph := float64(dw)*scale, float64(dh)*scale

	// page maps image space (u, v in 0..1, v up) to page points.
	page := func(u, v float64) (float64, float64) {
		c, r := u*float64(w), (1-v)*float64(h) // stored pixel: column, row from the top
		var x, yt float64                      // displayed pixel: column, row from the top
		W, H := float64(w), float64(h)
		switch orientation {
		case 1:
			x, yt = c, r
		case 2:
			x, yt = W-c, r
		case 3:
			x, yt = W-c, H-r
		case 4:
			x, yt = c, H-r
		case 5:
			x, yt = r, c
		case 6:
			x, yt = H-r, c
		case 7:
			x, yt = H-r, W-c
		case 8:
			x, yt = r, W-c
		}
		return x * scale, (float64(dh) - yt) * scale
	}
	e, f := page(0, 0)
	ax, ay := page(1, 0)
	cx, cy := page(0, 1)
	content := fmt.Sprintf("q %.4f %.4f %.4f %.4f %.4f %.4f cm /Im0 Do Q\n", ax-e, ay-f, cx-e, cy-f, e, f)

	var buf bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\n", len(offsets), body)
		if stream != nil {
			buf.WriteString("stream\n")
			buf.Write(stream)
			buf.WriteString("\nendstream\n")
		}
		buf.WriteString("endobj\n")
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", pw, ph), nil)
	obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>", w, h, colorSpace, len(jpg)), jpg)
	obj(fmt.Sprintf("<< /Length %d >>", len(content)), []byte(content))
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// exifOrientation reads the EXIF orientation tag (1–8) from a JPEG, or 1.
func exifOrientation(jpg []byte) int {
	r := bytes.NewReader(jpg)
	var marker [2]byte
	if _, err := io.ReadFull(r, marker[:]); err != nil || marker != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		if _, err := io.ReadFull(r, marker[:]); err != nil || marker[0] != 0xFF {
			return 1
		}
		if marker[1] == 0xDA || marker[1] == 0xD9 { // image data: no EXIF before it
			return 1
		}
		var size uint16
		if err := binary.Read(r, binary.BigEndian, &size); err != nil || size < 2 {
			return 1
		}
		seg := make([]byte, size-2)
		if _, err := io.ReadFull(r, seg); err != nil {
			return 1
		}
		if marker[1] == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
	}
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:8]))
	if ifd+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[ifd:]))
	for i := range n {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		if order.Uint16(t[e:]) == 0x0112 {
			if o := int(order.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}
