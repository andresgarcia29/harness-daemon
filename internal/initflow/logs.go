package initflow

import (
	"sync"
	"time"
)

// LogLine es una línea de bitácora de un paso, con secuencia global para que
// el SSE pueda reanudar desde Last-Event-ID sin perder nada (un brew install
// de 3 minutos no cabe en un tail).
type LogLine struct {
	Seq  int64  `json:"seq"`
	TS   int64  `json:"ts"`
	Step string `json:"step"`
	Line string `json:"line"`
}

const (
	perStepCap = 400 // ring por paso
	tailN      = 40  // lo que viaja en el snapshot
)

type LogBuffer struct {
	mu      sync.Mutex
	seq     int64
	perStep map[string][]LogLine
	all     []LogLine // ring global (para replay del SSE)
	subs    map[chan LogLine]struct{}
}

func NewLogBuffer() *LogBuffer {
	return &LogBuffer{perStep: map[string][]LogLine{}, subs: map[chan LogLine]struct{}{}}
}

func (l *LogBuffer) Append(step, line string) {
	l.mu.Lock()
	l.seq++
	ll := LogLine{Seq: l.seq, TS: time.Now().Unix(), Step: step, Line: line}
	ss := append(l.perStep[step], ll)
	if len(ss) > perStepCap {
		ss = ss[len(ss)-perStepCap:]
	}
	l.perStep[step] = ss
	l.all = append(l.all, ll)
	if len(l.all) > perStepCap*4 {
		l.all = l.all[len(l.all)-perStepCap*4:]
	}
	subs := make([]chan LogLine, 0, len(l.subs))
	for ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()
	for _, ch := range subs {
		select { // un suscriptor lento jamás bloquea al paso que loguea
		case ch <- ll:
		default:
		}
	}
}

// Tail devuelve las últimas n líneas de un paso (para el snapshot).
func (l *LogBuffer) Tail(step string, n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	ss := l.perStep[step]
	if len(ss) > n {
		ss = ss[len(ss)-n:]
	}
	out := make([]string, 0, len(ss))
	for _, ll := range ss {
		out = append(out, ll.Line)
	}
	return out
}

// Subscribe entrega el replay desde afterSeq y un canal vivo. cancel() SIEMPRE.
func (l *LogBuffer) Subscribe(afterSeq int64) (replay []LogLine, ch chan LogLine, cancel func()) {
	ch = make(chan LogLine, 256)
	l.mu.Lock()
	for _, ll := range l.all {
		if ll.Seq > afterSeq {
			replay = append(replay, ll)
		}
	}
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return replay, ch, func() {
		l.mu.Lock()
		delete(l.subs, ch)
		l.mu.Unlock()
	}
}
