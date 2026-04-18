package media

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ImageMeta struct {
	MimeType       string
	Width          int
	Height         int
	Is360          bool
	ProjectionType string
	CapturedAt     *time.Time
	ExifData       map[string]any
}

type VideoMeta struct {
	Width    int
	Height   int
	Duration int // seconds
	Is360    bool
}

// DetectMIME reads the first 512 bytes to detect MIME type.
// Returns the MIME type and a new reader that starts from the beginning.
func DetectMIME(r io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}
	mime := http.DetectContentType(buf[:n])
	combined := io.MultiReader(bytes.NewReader(buf[:n]), r)
	return mime, combined, nil
}

// IsAllowedMIME checks if the MIME type is allowed for upload.
func IsAllowedMIME(mime string) bool {
	allowed := map[string]bool{
		// Images
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
		"image/heic": true,
		"image/heif": true,
		// Videos
		"video/mp4":       true,
		"video/quicktime": true,
		"video/webm":      true,
		"video/ogg":       true,
		"video/x-msvideo": true,
	}
	return allowed[mime]
}

// MIMEFromExtension maps common file extensions to MIME types for cases where
// content sniffing returns "application/octet-stream" (e.g. some MOV/MP4 variants).
func MIMEFromExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".ogv", ".ogg":
		return "video/ogg"
	case ".avi":
		return "video/x-msvideo"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".heic", ".heif":
		return "image/heic"
	}
	return ""
}

// ExtractMetadata parses image data and returns metadata.
// Uses lightweight string search for XMP — avoids heavy EXIF library startup cost.
func ExtractMetadata(data []byte, mime string) *ImageMeta {
	meta := &ImageMeta{
		MimeType: mime,
		ExifData: make(map[string]any),
	}

	if mime == "image/jpeg" {
		extractJPEGMeta(data, meta)
	}

	return meta
}

// ExtractVideoMeta uses ffprobe to extract video dimensions, duration and 360 detection.
// filePath must be an absolute path to the saved video file.
func ExtractVideoMeta(filePath string) (*VideoMeta, error) {
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		filePath,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var probe struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			SideDataList []struct {
				SideDataType string `json:"side_data_type"`
				Projection   string `json:"projection"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}

	meta := &VideoMeta{}

	// Find first video stream for dimensions; also check spherical side-data
	// (Spherical Video RFC — used by GoPro, Ricoh Theta, DJI, YouTube 360 exports).
	for _, s := range probe.Streams {
		if s.CodecType == "video" {
			if s.Width > 0 && s.Height > 0 && meta.Width == 0 {
				meta.Width = s.Width
				meta.Height = s.Height
			}
			for _, sd := range s.SideDataList {
				if sd.SideDataType == "Spherical Mapping" &&
					strings.EqualFold(sd.Projection, "equirectangular") {
					meta.Is360 = true
				}
			}
		}
	}

	// Format-level tags (some encoders write spherical info here).
	if !meta.Is360 {
		for k, v := range probe.Format.Tags {
			kl, vl := strings.ToLower(k), strings.ToLower(v)
			if strings.Contains(kl, "spherical") || strings.Contains(vl, "equirectangular") {
				meta.Is360 = true
				break
			}
		}
	}

	// Parse duration (float seconds → round to nearest int)
	if probe.Format.Duration != "" {
		var dur float64
		fmt.Sscanf(probe.Format.Duration, "%f", &dur)
		meta.Duration = int(dur + 0.5)
	}

	// XMP UUID atom fallback — catches Insta360 and cameras that don't follow
	// the Spherical Video RFC but embed XMP in a custom MP4 UUID box.
	// Aspect ratio is intentionally NOT used: 16:9 (1.78) falls inside any
	// reasonable 2:1 tolerance window and causes false positives on every HD clip.
	if !meta.Is360 {
		meta.Is360 = detectVideoIs360FromFile(filePath)
	}

	return meta, nil
}

// detectVideoIs360FromFile reads up to 4 MB of the file looking for the XMP UUID
// atom (used by Insta360 and other 360 cameras in their MP4 exports).
func detectVideoIs360FromFile(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	// Read the first 4 MB — XMP atom is typically very early in the file
	data, err := io.ReadAll(io.LimitReader(f, 4*1024*1024))
	if err != nil || len(data) == 0 {
		return false
	}
	return detectIs360FromMP4Bytes(data)
}

// detectIs360FromMP4Bytes scans raw bytes for the XMP UUID box and checks for
// equirectangular markers. The UUID is: BE7ACFCB-97A9-42E8-9C71-999491E3AFAC
func detectIs360FromMP4Bytes(data []byte) bool {
	xmpUUID := []byte{0xBE, 0x7A, 0xCF, 0xCB, 0x97, 0xA9, 0x42, 0xE8, 0x9C, 0x71, 0x99, 0x94, 0x91, 0xE3, 0xAF, 0xAC}
	uuidType := []byte("uuid")

	i := 0
	for i+8 <= len(data) {
		boxSize := int(binary.BigEndian.Uint32(data[i : i+4]))
		if boxSize < 8 {
			i++
			continue
		}
		boxType := data[i+4 : i+8]
		if bytes.Equal(boxType, uuidType) && i+24 <= len(data) {
			uuid := data[i+8 : i+24]
			if bytes.Equal(uuid, xmpUUID) {
				end := i + boxSize
				if end > len(data) {
					end = len(data)
				}
				xmpContent := string(data[i+24 : end])
				if detectIs360FromXMP(xmpContent) {
					return true
				}
			}
		}
		if boxSize == 0 {
			break // box size 0 means extends to EOF
		}
		i += boxSize
	}
	return false
}

func extractJPEGMeta(data []byte, meta *ImageMeta) {
	xmpHeader := []byte("http://ns.adobe.com/xap/1.0/\x00")
	exifHeader := []byte("Exif\x00\x00")

	i := 0
	for i < len(data)-4 {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xE1 { // APP1
			if i+4 >= len(data) {
				break
			}
			segLen := int(data[i+2])<<8 | int(data[i+3])
			segEnd := i + 2 + segLen
			if segEnd > len(data) {
				segEnd = len(data)
			}
			segData := data[i+4 : segEnd]

			if len(segData) > len(xmpHeader) && bytes.Equal(segData[:len(xmpHeader)], xmpHeader) {
				xmpStr := string(segData[len(xmpHeader):])
				if detectIs360FromXMP(xmpStr) {
					meta.Is360 = true
					meta.ProjectionType = "equirectangular"
				}
			} else if meta.CapturedAt == nil && len(segData) > len(exifHeader) && bytes.Equal(segData[:len(exifHeader)], exifHeader) {
				meta.CapturedAt = extractExifDate(segData[len(exifHeader):])
			}
			i = segEnd
		} else if marker == 0xD8 || marker == 0xD9 {
			i += 2
		} else if marker == 0xD0 || marker == 0xD7 {
			i += 2
		} else {
			if i+4 > len(data) {
				break
			}
			segLen := int(data[i+2])<<8 | int(data[i+3])
			i += 2 + segLen
		}
	}
}

// extractExifDate parses DateTimeOriginal (tag 0x9003) from raw TIFF data
// (the bytes after the "Exif\x00\x00" header in a JPEG APP1 segment).
// Falls back to DateTimeDigitized (0x9004) then DateTime (0x0132).
// Returns nil when the date cannot be read or parsed.
func extractExifDate(tiff []byte) *time.Time {
	if len(tiff) < 8 {
		return nil
	}

	var order binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return nil
	}

	// TIFF magic check (0x002A)
	if order.Uint16(tiff[2:4]) != 0x002A {
		return nil
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))

	readUint16 := func(off int) (uint16, bool) {
		if off+2 > len(tiff) {
			return 0, false
		}
		return order.Uint16(tiff[off : off+2]), true
	}
	readUint32 := func(off int) (uint32, bool) {
		if off+4 > len(tiff) {
			return 0, false
		}
		return order.Uint32(tiff[off : off+4]), true
	}

	// readASCII reads a null-terminated ASCII value for a TIFF entry.
	readASCII := func(count, valOff int) string {
		if count <= 4 {
			// value fits inline in the 4-byte value field
			end := valOff + count
			if end > len(tiff) {
				end = len(tiff)
			}
			return strings.TrimRight(string(tiff[valOff:end]), "\x00")
		}
		offset, ok := readUint32(valOff)
		if !ok || int(offset)+count > len(tiff) {
			return ""
		}
		return strings.TrimRight(string(tiff[offset:int(offset)+count]), "\x00")
	}

	parseDate := func(s string) *time.Time {
		t, err := time.Parse("2006:01:02 15:04:05", strings.TrimSpace(s))
		if err != nil {
			return nil
		}
		return &t
	}

	// walkIFD searches for target tags in a TIFF IFD and returns their string values.
	walkIFD := func(offset int, targets map[uint16]bool) map[uint16]string {
		result := map[uint16]string{}
		count, ok := readUint16(offset)
		if !ok {
			return result
		}
		offset += 2
		for i := 0; i < int(count); i++ {
			entryOff := offset + i*12
			if entryOff+12 > len(tiff) {
				break
			}
			tag, _ := readUint16(entryOff)
			typ, _ := readUint16(entryOff + 2)
			cnt, _ := readUint32(entryOff + 4)
			valOff := entryOff + 8

			if targets[tag] && typ == 2 { // ASCII
				result[tag] = readASCII(int(cnt), valOff)
			}
		}
		return result
	}

	// Walk root IFD: find ExifIFD pointer (0x8769) and DateTime (0x0132).
	rootCount, ok := readUint16(ifdOffset)
	if !ok {
		return nil
	}
	var exifIFDOffset uint32
	var rootDateTime string

	for i := 0; i < int(rootCount); i++ {
		entryOff := ifdOffset + 2 + i*12
		if entryOff+12 > len(tiff) {
			break
		}
		tag, _ := readUint16(entryOff)
		valOff := entryOff + 8
		switch tag {
		case 0x8769: // ExifIFD pointer
			exifIFDOffset, _ = readUint32(valOff)
		case 0x0132: // DateTime
			typ, _ := readUint16(entryOff + 2)
			cnt, _ := readUint32(entryOff + 4)
			if typ == 2 {
				rootDateTime = readASCII(int(cnt), valOff)
			}
		}
	}

	// Prefer DateTimeOriginal (0x9003) from ExifIFD.
	if exifIFDOffset > 0 {
		exifTags := walkIFD(int(exifIFDOffset), map[uint16]bool{0x9003: true, 0x9004: true})
		if s := exifTags[0x9003]; s != "" {
			return parseDate(s)
		}
		if s := exifTags[0x9004]; s != "" {
			return parseDate(s)
		}
	}

	// Last resort: root DateTime.
	if rootDateTime != "" {
		return parseDate(rootDateTime)
	}
	return nil
}

func detectIs360FromXMP(xmp string) bool {
	lower := strings.ToLower(xmp)
	if strings.Contains(lower, "projectiontype") && strings.Contains(lower, "equirectangular") {
		return true
	}
	if strings.Contains(lower, "usepanoramaviewer") && strings.Contains(lower, "true") {
		return true
	}
	if strings.Contains(lower, "equirectangular") {
		return true
	}
	if strings.Contains(lower, "insta360") {
		return true
	}
	return false
}

// Detect360FromAspectRatio falls back to aspect ratio check (2:1 ± 15%).
func Detect360FromAspectRatio(width, height int) bool {
	if height == 0 {
		return false
	}
	ratio := float64(width) / float64(height)
	return ratio >= 1.7 && ratio <= 2.3
}
