package capture

import "ashgate-lite/internal/proxycore/capture/process"

// decision is where one process's traffic goes.
type decision struct {
	proxy  string // "" with direct=false: no rule matched
	direct bool
	// key names the rule (or OthersKey) for statistics.
	key string
	// parent names the ancestor whose rule was inherited.
	parent string
}

func (d decision) matched() bool { return d.direct || d.proxy != "" }

type compiledRule struct {
	path  string
	proxy string
}

// ruleSet is an immutable, ordered list of rules plus the default route.
type ruleSet struct {
	rules []compiledRule
	def   string
}

func compileRules(plan Plan) *ruleSet {
	out := &ruleSet{rules: make([]compiledRule, 0, len(plan.Rules)), def: plan.Default}
	for _, r := range plan.Rules {
		path := process.NormalizePath(r.Path)
		if path == "" {
			continue
		}
		out.rules = append(out.rules, compiledRule{path: path, proxy: r.ProxyID})
	}
	return out
}

// match returns the first rule for p. When no rule names p itself, the
// nearest ancestor with a rule decides, so helpers an application starts
// (updaters, Electron and browser helpers) follow the application. The
// climb stops at shell and system processes; otherwise a rule could spread
// to everything started from the desktop. Unmatched processes take the
// default route, except the Windows service host, which carries system
// traffic for every program.
func (s *ruleSet) match(p process.Process) decision {
	if s == nil {
		return decision{}
	}
	if r, ok := s.first(p); ok {
		return r.decision("")
	}
	for _, a := range p.Parents {
		if isShellOrSystem(a) {
			break
		}
		if r, ok := s.first(a); ok {
			return r.decision(displayName(a))
		}
	}
	if s.def == "" || process.Base(displayName(p)) == "svchost.exe" {
		return decision{}
	}
	return decision{proxy: s.def, key: OthersKey}
}

func (r compiledRule) decision(parent string) decision {
	return decision{proxy: r.proxy, direct: r.proxy == "", key: r.path, parent: parent}
}

func (s *ruleSet) first(p process.Process) (compiledRule, bool) {
	path := process.NormalizePath(p.Path)
	if path == "" {
		return compiledRule{}, false
	}
	for _, r := range s.rules {
		if r.path == path {
			return r, true
		}
	}
	return compiledRule{}, false
}

// shellOrSystem are processes that start unrelated programs. Rules are
// never inherited through them.
var shellOrSystem = map[string]bool{
	"explorer.exe": true, "svchost.exe": true, "services.exe": true, "wininit.exe": true,
	"winlogon.exe": true, "userinit.exe": true, "smss.exe": true, "csrss.exe": true,
	"lsass.exe": true, "system": true, "sihost.exe": true, "taskhostw.exe": true,
	"runtimebroker.exe": true, "dllhost.exe": true, "wmiprvse.exe": true, "taskeng.exe": true,
	"startmenuexperiencehost.exe": true, "shellexperiencehost.exe": true,
	"searchhost.exe": true, "applicationframehost.exe": true, "ashgate-lite.exe": true,
}

func isShellOrSystem(p process.Process) bool {
	return shellOrSystem[process.Base(displayName(p))]
}

func displayName(p process.Process) string {
	if p.Name != "" {
		return p.Name
	}
	return process.Base(p.Path)
}
