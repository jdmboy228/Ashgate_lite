package windivert

import (
	"syscall"
	"testing"
)

func TestFormatError(t *testing.T) {
	got := FormatError(syscall.Errno(5))
	if got == "" || !contains(got, "администратора") || !contains(got, "5") {
		t.Fatal(got)
	}
	if FormatError(nil) != "" {
		t.Fatal("nil")
	}
	unknown := FormatError(syscall.Errno(99999))
	if !contains(unknown, "99999") {
		t.Fatal(unknown)
	}
	if !IsNoData(syscall.Errno(232)) {
		t.Fatal("expected no data")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
