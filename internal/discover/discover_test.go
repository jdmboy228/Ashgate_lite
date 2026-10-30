package discover

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// buildLink makes a minimal .lnk with a LinkInfo that has both ANSI and
// Unicode local base paths.
func buildLink(ansi, wide string) []byte {
	head := make([]byte, lnkHeaderSize)
	binary.LittleEndian.PutUint32(head, lnkHeaderSize)
	binary.LittleEndian.PutUint32(head[0x14:], hasLinkInfo)
	info := make([]byte, unicodeInfoHeader)
	binary.LittleEndian.PutUint32(info[4:], unicodeInfoHeader)
	binary.LittleEndian.PutUint32(info[8:], volumeIDAndPath)
	binary.LittleEndian.PutUint32(info[0x10:], uint32(len(info)))
	info = append(info, append([]byte(ansi), 0)...)
	binary.LittleEndian.PutUint32(info[0x1C:], uint32(len(info)))
	for _, c := range utf16.Encode([]rune(wide)) {
		info = binary.LittleEndian.AppendUint16(info, c)
	}
	info = append(info, 0, 0)
	binary.LittleEndian.PutUint32(info, uint32(len(info)))
	return append(head, info...)
}

func TestLinkTargetPrefersUnicodePath(t *testing.T) {
	got := linkTarget(buildLink(`C:\Apps\x.exe`, `C:\Программы\Telegram.exe`))
	if got != `C:\Программы\Telegram.exe` {
		t.Fatalf("%q", got)
	}
	if linkTarget([]byte("not a link")) != "" {
		t.Fatal("garbage parsed")
	}
}

type fakeOS struct{ run, menu, files []app }

func (f fakeOS) running() []app   { return f.run }
func (f fakeOS) shortcuts() []app { return f.menu }
func (f fakeOS) installed() []app { return f.files }

func TestSuggestOrdersSourcesAndDedupes(t *testing.T) {
	f := &Finder{os: fakeOS{
		run:   []app{{"Dolphin Anty", `C:\U\Dolphin Anty.exe`}},
		menu:  []app{{"Dolphin Updater", `C:\U\updater.exe`}, {"Dolphin Anty", `C:\U\DOLPHIN ANTY.exe`}},
		files: []app{{"dolphin_anty", `C:\Program Files\Beta\dolphin_anty.exe`}, {"chrome", `C:\Chrome\chrome.exe`}},
	}}
	got := f.Suggest("dolph")
	if len(got) != 3 || got[0].Source != sourceRunning || got[1].Name != "Dolphin Updater" || got[2].Source != sourceProgramFiles {
		t.Fatalf("%+v", got)
	}
	if first := f.Suggest(""); len(first) != 2 {
		t.Fatalf("first-run list must skip Program Files: %+v", first)
	}
}
