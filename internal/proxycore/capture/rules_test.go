package capture

import (
	"testing"

	"ashgate-lite/internal/proxycore/capture/process"
)

func testRules() *ruleSet {
	return compileRules(Plan{Rules: []Rule{
		{Path: `C:\Apps\Curl.exe`, ProxyID: "p1"},
		{Path: `C:\Apps\direct.exe`},
	}})
}

func TestMatchByPathIgnoresCase(t *testing.T) {
	got := testRules().match(process.Process{Path: `c:/apps/curl.EXE`})
	if got.proxy != "p1" || got.key != `c:\apps\curl.exe` {
		t.Fatalf("%+v", got)
	}
	if d := testRules().match(process.Process{Path: `C:\Apps\direct.exe`}); !d.direct || d.proxy != "" {
		t.Fatalf("explicit direct %+v", d)
	}
	if d := testRules().match(process.Process{Path: `C:\Other\curl.exe`}); d.matched() {
		t.Fatalf("other path matched %+v", d)
	}
}

func TestChildInheritsUntilShell(t *testing.T) {
	parent := process.Process{Name: "curl.exe", Path: `C:\Apps\curl.exe`}
	child := process.Process{Name: "helper.exe", Path: `C:\Apps\helper.exe`, Parents: []process.Process{parent}}
	if d := testRules().match(child); d.proxy != "p1" || d.parent != "curl.exe" {
		t.Fatalf("child %+v", d)
	}
	viaShell := process.Process{Path: `C:\x\app.exe`, Parents: []process.Process{{Name: "explorer.exe"}, parent}}
	if d := testRules().match(viaShell); d.matched() {
		t.Fatalf("rule crossed the shell %+v", d)
	}
}

func TestDefaultRouteSkipsServiceHost(t *testing.T) {
	s := compileRules(Plan{Default: "p2", Rules: []Rule{{Path: `C:\Apps\direct.exe`}}})
	if d := s.match(process.Process{Path: `C:\Any\notepad.exe`}); d.proxy != "p2" || d.key != OthersKey {
		t.Fatalf("default %+v", d)
	}
	if d := s.match(process.Process{Path: `C:\Apps\direct.exe`}); !d.direct {
		t.Fatalf("listed direct app took the default route %+v", d)
	}
	if d := s.match(process.Process{Name: "svchost.exe", Path: `C:\Windows\System32\svchost.exe`}); d.matched() {
		t.Fatalf("svchost routed %+v", d)
	}
}
