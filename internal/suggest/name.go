package suggest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reCodecSwap = regexp.MustCompile(`(?i)\bx264\b`)
	reHevcMark  = regexp.MustCompile(`(?i)\b(x265|hevc|h\.?265)\b`)
	reAnyCodec  = regexp.MustCompile(`(?i)\b(x264|h\.?264|avc|x265|hevc|h\.?265|av1)\b`)
	reResTag    = regexp.MustCompile(`(?i)\b(4320p|2160p|1440p|1080p|720p|480p|360p|4k|uhd|fhd|hd)\b`)
)

// Codec tags for OutputName.
const (
	TagNone = ""
	TagHEVC = "x265"
	TagAV1  = "AV1"
)

// OutputName builds a suggested output filename from the source path,
// preferred container extension, codec tag (TagHEVC, TagAV1 or TagNone for
// a stream copy), and optional target height.
func OutputName(inputPath, container, codecTag string, scaleHeight int) string {
	dir := filepath.Dir(inputPath)
	base := filepath.Base(inputPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	switch codecTag {
	case TagAV1:
		if reAnyCodec.MatchString(name) {
			name = reAnyCodec.ReplaceAllString(name, TagAV1)
		} else {
			name = name + "." + TagAV1
		}
	case TagHEVC:
		if reCodecSwap.MatchString(name) {
			name = reCodecSwap.ReplaceAllString(name, "x265")
		} else if !reHevcMark.MatchString(name) {
			name = name + ".x265"
		}
	}

	if scaleHeight > 0 {
		tag := resolutionTag(scaleHeight)
		if reResTag.MatchString(name) {
			name = reResTag.ReplaceAllString(name, tag)
		} else {
			name = name + "." + tag
		}
	}

	outExt := normalizeExt(container)
	return filepath.Join(dir, name+outExt)
}

func resolutionTag(height int) string {
	switch {
	case height >= 2160:
		return "2160p"
	case height >= 1440:
		return "1440p"
	case height >= 1080:
		return "1080p"
	case height >= 720:
		return "720p"
	case height >= 480:
		return "480p"
	default:
		return fmt.Sprintf("%dp", height)
	}
}

func normalizeExt(container string) string {
	c := strings.ToLower(strings.TrimSpace(container))
	c = strings.TrimPrefix(c, ".")
	switch c {
	case "mp4":
		return ".mp4"
	case "mov":
		return ".mov"
	default:
		return ".mkv"
	}
}
