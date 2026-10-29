package process

import (
	"sync"
	"time"

	"ashgate-lite/internal/proxycore/capture/packet"
)

const (
	closeGrace = 15 * time.Second
	helperGap  = 50 * time.Millisecond
	// pruneEvery bounds how often Observe drops expired entries. Without it
	// every socket the machine ever closed would stay in memory.
	pruneEvery = 30 * time.Second
	// maxImages caps the PID cache when the platform cannot tell whether a
	// process has exited.
	maxImages = 4096
	// maxParents bounds the ancestor chain.
	maxParents = 8
)

type exactKey struct {
	proto uint8
	addr  packet.Addr
}

type wildKey struct {
	proto uint8
	port  uint16
}

type entry struct {
	proc  Process
	until time.Time
}

// Resolver remembers SOCKET/FLOW events and falls back to the IP Helper tables.
// A socket event wins over the helper table so a stale table cannot replace it.
type Resolver struct {
	mu         sync.Mutex
	exact      map[exactKey]entry
	wild       map[wildKey]entry
	helper     map[exactKey]uint32
	helperWild map[wildKey]uint32
	images     map[uint32]Process
	helperAt   time.Time
	pruneAt    time.Time
	now        func() time.Time
	image      func(pid uint32) Process
	loadHelper func() ([]Endpoint, error)
	// exited reports that pid no longer names the process cached for it.
	// Windows reuses PIDs, so a cached name must be checked before reuse.
	// Nil means the platform cannot tell; the cache is then only size-capped.
	exited func(pid uint32) bool
	// release frees platform resources, such as held process handles.
	release func()
}

// Close releases process handles held for the PID cache.
func (r *Resolver) Close() {
	if r != nil && r.release != nil {
		r.release()
	}
}

// New returns an empty resolver. On Windows it also queries IP Helper.
func New() *Resolver {
	r := &Resolver{
		exact:      map[exactKey]entry{},
		wild:       map[wildKey]entry{},
		helper:     map[exactKey]uint32{},
		helperWild: map[wildKey]uint32{},
		images:     map[uint32]Process{},
		now:        time.Now,
		image: func(pid uint32) Process {
			return Process{PID: pid}
		},
	}
	configure(r)
	return r
}

// Observe records a SOCKET or FLOW event. closing keeps the entry for closeGrace.
func (r *Resolver) Observe(proto uint8, local packet.Addr, pid uint32, closing bool) {
	if r == nil || pid == 0 || local.Port == 0 || (proto != packet.ProtoTCP && proto != packet.ProtoUDP) {
		return
	}
	now := r.now()
	proc := r.resolve(pid)
	e := entry{proc: proc}
	if closing {
		e.until = now.Add(closeGrace)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if unspecified(local) {
		r.wild[wildKey{proto, local.Port}] = e
	} else {
		r.exact[exactKey{proto, local}] = e
	}
	if r.pruneAt.IsZero() {
		r.pruneAt = now
	} else if now.Sub(r.pruneAt) >= pruneEvery {
		r.pruneLocked(now)
	}
}

// pruneLocked drops closed sockets past their grace period, sockets of
// processes that have exited (their CLOSE event may have been lost), and
// cached images of exited processes.
func (r *Resolver) pruneLocked(now time.Time) {
	r.pruneAt = now
	gone := map[uint32]bool{}
	dead := func(pid uint32) bool {
		if r.exited == nil {
			return false
		}
		d, ok := gone[pid]
		if !ok {
			d = r.exited(pid)
			gone[pid] = d
		}
		return d
	}
	for k, e := range r.exact {
		if !alive(e, now) || dead(e.proc.PID) {
			delete(r.exact, k)
		}
	}
	for k, e := range r.wild {
		if !alive(e, now) || dead(e.proc.PID) {
			delete(r.wild, k)
		}
	}
	for pid := range r.images {
		if dead(pid) {
			delete(r.images, pid)
		}
	}
	if len(r.images) > maxImages {
		clear(r.images)
	}
}

// Lookup implements engine.Resolver.
func (r *Resolver) Lookup(proto uint8, src packet.Addr) (Process, bool) {
	if r == nil || src.Port == 0 {
		return Process{}, false
	}
	now := r.now()
	r.mu.Lock()
	if p, ok := r.socketLocked(proto, src, now); ok {
		r.mu.Unlock()
		return r.finish(p)
	}
	need := r.loadHelper != nil && (r.helperAt.IsZero() || now.Sub(r.helperAt) >= helperGap)
	r.mu.Unlock()

	if need {
		rows, err := r.loadHelper()
		r.mu.Lock()
		if err == nil {
			r.storeHelper(rows, now)
		} else if r.helperAt.IsZero() || now.Sub(r.helperAt) >= helperGap {
			r.helperAt = now
		}
		if p, ok := r.socketLocked(proto, src, now); ok {
			r.mu.Unlock()
			return r.finish(p)
		}
		p, ok := r.helperLocked(proto, src)
		r.mu.Unlock()
		if !ok {
			return Process{}, false
		}
		return r.finish(p)
	}

	r.mu.Lock()
	p, ok := r.helperLocked(proto, src)
	r.mu.Unlock()
	if !ok {
		return Process{}, false
	}
	return r.finish(p)
}

func (r *Resolver) finish(p Process) (Process, bool) {
	if p.Name == "" && p.Path == "" {
		p = r.resolve(p.PID)
	}
	if p.Name == "" && p.Path == "" {
		return Process{}, false
	}
	return p, true
}

func (r *Resolver) socketLocked(proto uint8, src packet.Addr, now time.Time) (Process, bool) {
	if e, ok := r.exact[exactKey{proto, src}]; ok {
		if alive(e, now) {
			return e.proc, true
		}
		delete(r.exact, exactKey{proto, src})
	}
	wk := wildKey{proto, src.Port}
	if e, ok := r.wild[wk]; ok {
		if alive(e, now) {
			return e.proc, true
		}
		delete(r.wild, wk)
	}
	return Process{}, false
}

// helperLocked returns only the PID. finish resolves the name through
// resolve, which checks that the cached image still belongs to that PID.
func (r *Resolver) helperLocked(proto uint8, src packet.Addr) (Process, bool) {
	if pid, ok := r.helper[exactKey{proto, src}]; ok {
		return Process{PID: pid}, true
	}
	if pid, ok := r.helperWild[wildKey{proto, src.Port}]; ok {
		return Process{PID: pid}, true
	}
	return Process{}, false
}

func (r *Resolver) storeHelper(rows []Endpoint, now time.Time) {
	h := make(map[exactKey]uint32, len(rows))
	w := map[wildKey]uint32{}
	for _, row := range rows {
		if row.PID == 0 || row.Addr.Port == 0 {
			continue
		}
		if row.Unspecified {
			w[wildKey{row.Proto, row.Addr.Port}] = row.PID
			continue
		}
		h[exactKey{row.Proto, row.Addr}] = row.PID
	}
	r.helper = h
	r.helperWild = w
	r.helperAt = now
}

func (r *Resolver) resolve(pid uint32) Process {
	return r.resolveAt(pid, 0)
}

// resolveAt resolves pid and, below maxParents, its ancestor chain. The
// chain is computed once, when the PID is first cached: a parent never
// changes, and it may exit later.
func (r *Resolver) resolveAt(pid uint32, depth int) Process {
	r.mu.Lock()
	p, ok := r.images[pid]
	r.mu.Unlock()
	if ok && (p.Name != "" || p.Path != "") {
		if r.exited == nil || !r.exited(pid) {
			return p
		}
		// The process is gone and the PID may now belong to another one.
		r.mu.Lock()
		if cur, ok := r.images[pid]; ok && cur.Started == p.Started && cur.Path == p.Path {
			delete(r.images, pid)
		}
		r.mu.Unlock()
	}
	if r.image == nil {
		return Process{PID: pid}
	}
	got := r.image(pid)
	got.PID = pid
	if got.Name == "" && got.Path != "" {
		got.Name = Base(got.Path)
	}
	if got.Name == "" && got.Path == "" {
		return got
	}
	if depth < maxParents {
		got.Parents = r.parents(got, depth)
	}
	r.mu.Lock()
	r.images[pid] = got
	r.mu.Unlock()
	return got
}

// parents returns child's ancestors, nearest first. A parent that started
// after the child holds a reused PID and ends the chain, as does one that
// has exited or cannot be opened.
func (r *Resolver) parents(child Process, depth int) []Process {
	if child.PPID == 0 || child.PPID == child.PID {
		return nil
	}
	par := r.resolveAt(child.PPID, depth+1)
	if par.Name == "" && par.Path == "" {
		return nil
	}
	if par.Started != 0 && child.Started != 0 && par.Started > child.Started {
		return nil
	}
	out := make([]Process, 0, 1+len(par.Parents))
	flat := par
	flat.Parents = nil
	out = append(out, flat)
	for _, a := range par.Parents {
		if len(out) >= maxParents || a.PID == child.PID {
			break
		}
		out = append(out, a)
	}
	return out
}

func alive(e entry, now time.Time) bool {
	return e.until.IsZero() || now.Before(e.until)
}

func unspecified(a packet.Addr) bool {
	for i := uint8(0); i < a.Len; i++ {
		if a.IP[i] != 0 {
			return false
		}
	}
	return a.Len == 4 || a.Len == 16
}

// SetClock overrides the clock. Tests use it.
func (r *Resolver) SetClock(now func() time.Time) {
	if now != nil {
		r.now = now
	}
}

// SetImage overrides process-image lookup. Tests use it.
func (r *Resolver) SetImage(fn func(pid uint32) Process) {
	r.image = fn
}

// SetExited overrides the process-exit check. Tests use it.
func (r *Resolver) SetExited(fn func(pid uint32) bool) {
	r.exited = fn
}

// SetHelper overrides the IP Helper refresh. Tests use it.
func (r *Resolver) SetHelper(fn func() ([]Endpoint, error)) {
	r.loadHelper = fn
}
