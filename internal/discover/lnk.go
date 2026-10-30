package discover

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Shell link (.lnk) layout, MS-SHLLINK: a 76-byte header, an optional
// item ID list, then LinkInfo with the target's local path.
const (
	lnkHeaderSize     = 0x4C
	hasTargetIDList   = 0x01
	hasLinkInfo       = 0x02
	volumeIDAndPath   = 0x01
	unicodeInfoHeader = 0x24
)

// linkTarget returns the local target path of a shortcut, or "".
func linkTarget(b []byte) string {
	if len(b) < lnkHeaderSize || binary.LittleEndian.Uint32(b) != lnkHeaderSize {
		return ""
	}
	flags := binary.LittleEndian.Uint32(b[0x14:])
	off := lnkHeaderSize
	if flags&hasTargetIDList != 0 {
		if len(b) < off+2 {
			return ""
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
	}
	if flags&hasLinkInfo == 0 || len(b) < off+0x1C {
		return ""
	}
	info := b[off:]
	size := int(binary.LittleEndian.Uint32(info))
	if size > len(info) || size < 0x1C {
		return ""
	}
	info = info[:size]
	headerSize := binary.LittleEndian.Uint32(info[4:])
	if binary.LittleEndian.Uint32(info[8:])&volumeIDAndPath == 0 {
		return ""
	}
	if headerSize >= unicodeInfoHeader && size >= unicodeInfoHeader {
		base := wideAt(info, binary.LittleEndian.Uint32(info[0x1C:]))
		suffix := wideAt(info, binary.LittleEndian.Uint32(info[0x20:]))
		return joinSuffix(base, suffix)
	}
	base := ansiAt(info, binary.LittleEndian.Uint32(info[0x10:]))
	suffix := ansiAt(info, binary.LittleEndian.Uint32(info[0x18:]))
	return joinSuffix(base, suffix)
}

func joinSuffix(base, suffix string) string {
	if base == "" || suffix == "" {
		return base
	}
	return strings.TrimRight(base, `\`) + `\` + suffix
}

func ansiAt(b []byte, off uint32) string {
	if off == 0 || int(off) >= len(b) {
		return ""
	}
	s := b[off:]
	if i := strings.IndexByte(string(s), 0); i >= 0 {
		s = s[:i]
	}
	return string(s)
}

func wideAt(b []byte, off uint32) string {
	if off == 0 || int(off)+1 >= len(b) {
		return ""
	}
	var u []uint16
	for i := int(off); i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}
