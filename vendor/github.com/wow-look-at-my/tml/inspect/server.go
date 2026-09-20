package inspect

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/tml/layout"
)

// Server answers inspection requests about a live program. A single handler serves a pair of transports. A unix socket carries
type Server struct {
	source Source
	// control is the source when it can also be driven. A nil a single answers every write operation with a reason rather than
	control Controller

	mu       sync.Mutex
	closed   bool
	conns    []net.Listener
	sockPath string

	// waiters are the connections parked on op=frame with a since. Publish wakes them.
	waitMu  sync.Mutex
	waiters []chan struct{}
}

// frameWait is how long op=frame parks before answering that nothing new was painted. It is short on purpose: the
const frameWait = 2 * time.Second

// NewServer returns a server reading from source. When source also implements Controller, the driving operations are
func NewServer(source Source) *Server {
	s := &Server{source: source}
	if c, ok := source.(Controller); ok {
		s.control = c
	}
	return s
}

// Publish wakes anything waiting for a newer frame. A host calls it after each paint; forgetting to call it costs
func (s *Server) Publish() {
	s.waitMu.Lock()
	for _, ch := range s.waiters {
		close(ch)
	}
	s.waiters = nil
	s.waitMu.Unlock()
}

// wait returns a channel closed by the next Publish.
func (s *Server) wait() chan struct{} {
	ch := make(chan struct{})
	s.waitMu.Lock()
	s.waiters = append(s.waiters, ch)
	s.waitMu.Unlock()
	return ch
}

// ListenSocket serves the line protocol on a unix socket at path. A stale socket left by a killed program is removed
func (s *Server) ListenSocket(path string) error {
	if path == "" {
		return errors.New("inspect: socket path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("inspect: cannot create socket directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if probe, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
			probe.Close()
			return fmt.Errorf("inspect: %s is already served by a live program", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("inspect: cannot remove stale socket %s: %w", path, err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("inspect: cannot listen on %s: %w", path, err)
	}
	s.mu.Lock()
	s.conns = append(s.conns, ln)
	s.sockPath = path
	s.mu.Unlock()
	go s.acceptLoop(ln)
	return nil
}

// ListenHTTP serves the browser inspector on addr and returns the URL it landed on. An empty addr takes an ephemeral
func (s *Server) ListenHTTP(addr string) (string, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("inspect: cannot listen on %s: %w", addr, err)
	}
	s.mu.Lock()
	s.conns = append(s.conns, ln)
	s.mu.Unlock()

	srv := &http.Server{Handler: s.httpHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), nil
}

// Close stops every listener and removes the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var first error
	for _, ln := range s.conns {
		if err := ln.Close(); err != nil && first == nil {
			first = err
		}
	}
	if s.sockPath != "" {
		if err := os.Remove(s.sockPath); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}
	s.Publish()
	return first
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // the listener is closed; Close owns the error
		}
		go s.serveConn(conn)
	}
}

// serveConn answers a single request per line for as long as the client keeps the connection open.
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(Response{Error: fmt.Sprintf("request is not JSON: %v", err)})
			continue
		}
		if err := enc.Encode(s.Handle(req)); err != nil {
			return
		}
	}
}

// Handle answers a single request. It is the whole protocol: both transports call this and nothing else.
func (s *Server) Handle(req Request) Response {
	switch req.Op {
	case "query":
		return s.query(req)
	case "elements":
		return s.elements(req)
	case "ids":
		return s.ids()
	case "tree":
		return s.tree()
	case "frame":
		return s.frame(req)
	case "at":
		return s.at(req)
	case "hits":
		return s.hits()
	case "key":
		return s.key(req)
	case "click":
		return s.click(req)
	case "restyle":
		return s.restyle(req)
	case "reset":
		return s.reset()
	case "":
		return Response{Error: "no op given; want one of " + join(Ops)}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q; want one of %s", req.Op, join(Ops))}
	}
}

// current returns the frame on screen, or the reason there is none. A program that has not painted yet is a real
func (s *Server) current() (Frame, error) {
	f, ok := s.source.Frame()
	if !ok {
		return Frame{}, errors.New("the program has not painted a frame yet")
	}
	if f.Box == nil {
		return Frame{}, errors.New("the frame carries no layout")
	}
	return f, nil
}

func (s *Server) query(req Request) Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	if req.ID == "" {
		return Response{Error: "query needs an id; use op=elements for all of them"}
	}
	hit := Find(f.Box, req.ID)
	if hit == nil {
		known := IDs(f.Box)
		if len(known) == 0 {
			return Response{Error: fmt.Sprintf("no element has id %q: this frame declares no ids at all", req.ID)}
		}
		return Response{Error: fmt.Sprintf("no element has id %q: the frame declares %s", req.ID, join(known))}
	}
	el := Describe(hit, f.State, Options{ANSI: req.ANSI})
	return Response{Element: &el}
}

func (s *Server) elements(req Request) Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Elements: Elements(f.Box, f.State, Options{ANSI: req.ANSI})}
}

func (s *Server) ids() Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	ids := IDs(f.Box)
	if ids == nil {
		ids = []string{}
	}
	return Response{IDs: ids}
}

func (s *Server) tree() Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	t := Tree(f.Box, f.State)
	return Response{Tree: &t}
}

// frame reports the painted frame. With Since it waits for a newer frame, so a watcher follows the program instead of
func (s *Server) frame(req Request) Response {
	deadline := time.After(frameWait)
	for {
		f, err := s.current()
		if err != nil {
			return Response{Error: err.Error()}
		}
		if req.Since == 0 || f.Seq > req.Since {
			info := FrameInfo{
				Seq: f.Seq, At: f.At.UTC().Format(time.RFC3339Nano),
				Width: f.Width, Height: f.Height,
				Text: stripANSI(f.ANSI),
			}
			if req.ANSI {
				info.ANSI = f.ANSI
			}
			return Response{Frame: &info}
		}
		ch := s.wait()
		select {
		case <-ch:
		case <-deadline:
			return Response{Error: fmt.Sprintf("no frame newer than %d within %s", req.Since, frameWait)}
		}
	}
}

func (s *Server) at(req Request) Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	id := At(f.Box, req.X, req.Y)
	return Response{Hit: id, Found: id != ""}
}

// hits answers at for every cell in a single response. A reader with no way to call back -- a written page -- resolves
// a cell by looking it up here, rather than carrying its own copy of what covers what and drifting from this.
func (s *Server) hits() Response {
	f, err := s.current()
	if err != nil {
		return Response{Error: err.Error()}
	}
	where := map[string]int{}
	for i, id := range IDs(f.Box) {
		where[id] = i
	}
	rows := make([][]int, f.Height)
	for y := range rows {
		row := make([]int, f.Width)
		for x := range row {
			row[x] = -1
			if id := At(f.Box, x, y); id != "" {
				row[x] = where[id]
			}
		}
		rows[y] = row
	}
	return Response{Hits: rows}
}

func (s *Server) key(req Request) Response {
	if s.control == nil {
		return Response{Error: "this program is read-only: it exposes a frame but accepts no input"}
	}
	if req.Key == "" {
		return Response{Error: "key needs a key name, such as enter or ctrl+c"}
	}
	if err := s.control.Key(req.Key); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

func (s *Server) click(req Request) Response {
	if s.control == nil {
		return Response{Error: "this program is read-only: it exposes a frame but accepts no input"}
	}
	if err := s.control.Click(req.X, req.Y); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

func (s *Server) restyle(req Request) Response {
	if s.control == nil {
		return Response{Error: "this program is read-only: its styles cannot be overridden"}
	}
	if req.ID == "" {
		return Response{Error: "restyle needs an id"}
	}
	if len(req.Attrs) == 0 {
		return Response{Error: "restyle needs at least one attribute; use op=reset to drop overrides"}
	}
	if err := s.control.Restyle(req.ID, req.Attrs); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

func (s *Server) reset() Response {
	if s.control == nil {
		return Response{Error: "this program is read-only: it has no overrides to drop"}
	}
	if err := s.control.Reset(); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

// stateOf is a convenience for a host building a Frame: it turns the targets the last layout published into the map
func StateOf(targets []layout.Target) map[string]layout.Target {
	out := make(map[string]layout.Target, len(targets))
	for _, t := range targets {
		out[t.ID] = t
	}
	return out
}
