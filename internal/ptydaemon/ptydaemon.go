package ptydaemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

const socketName = "ptyd.sock"
const maxRingBytes = 256 * 1024

type Request struct {
	Op      string   `json:"op"`
	ID      string   `json:"id,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	Command []string `json:"command,omitempty"`
	Rows    uint16   `json:"rows,omitempty"`
	Cols    uint16   `json:"cols,omitempty"`
}

type Response struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Socket   string    `json:"socket,omitempty"`
	Sessions []Session `json:"sessions,omitempty"`
}

type Session struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd"`
	Command   []string  `json:"command"`
	PID       int       `json:"pid,omitempty"`
	Running   bool      `json:"running"`
	ExitError string    `json:"exit_error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Clients   int       `json:"clients"`
}

type Server struct {
	mu       sync.Mutex
	sessions map[string]*ptySession
}

type ptySession struct {
	Session
	pty     *os.File
	ring    []byte
	clients map[net.Conn]bool
}

func SocketPath() string {
	if dir := strings.TrimSpace(os.Getenv("GLINT_PTYD_SOCKET")); dir != "" {
		if strings.HasSuffix(dir, ".sock") {
			return dir
		}
		return filepath.Join(dir, socketName)
	}
	if dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); dir != "" {
		return filepath.Join(dir, "glint", socketName)
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("glint-%d", os.Getuid()), socketName)
}

func RunServer() error {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = os.Chmod(path, 0o600)
	fmt.Fprintf(os.Stderr, "glint ptyd listening on %s\n", path)

	s := &Server{sessions: map[string]*ptySession{}}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, Response{Error: err.Error()})
		return
	}
	switch req.Op {
	case "ping":
		writeResponse(conn, Response{OK: true, Socket: SocketPath()})
	case "start":
		resp := s.start(req)
		writeResponse(conn, resp)
	case "list":
		writeResponse(conn, Response{OK: true, Sessions: s.list()})
	case "kill":
		writeResponse(conn, s.kill(req.ID))
	case "detach":
		writeResponse(conn, s.detach(req.ID))
	case "attach":
		s.attach(conn, req)
	case "resize":
		writeResponse(conn, s.resize(req))
	default:
		writeResponse(conn, Response{Error: "unknown op " + req.Op})
	}
}

func (s *Server) start(req Request) Response {
	id := cleanID(req.ID)
	if id == "" {
		return Response{Error: "id is required"}
	}
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return Response{Error: "command is required"}
	}
	s.mu.Lock()
	if existing := s.sessions[id]; existing != nil && existing.Running {
		s.mu.Unlock()
		return Response{Error: "session already running: " + id}
	}
	s.mu.Unlock()

	cmd := exec.Command(req.Command[0], req.Command[1:]...)
	if req.CWD != "" {
		cmd.Dir = req.CWD
	}
	cmd.Env = agentEnv(id, req.CWD)
	size := &pty.Winsize{Rows: max(req.Rows, 24), Cols: max(req.Cols, 80)}
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return Response{Error: err.Error()}
	}
	ps := &ptySession{
		Session: Session{ID: id, CWD: req.CWD, Command: append([]string(nil), req.Command...), PID: cmd.Process.Pid, Running: true, StartedAt: time.Now()},
		pty:     ptmx,
		clients: map[net.Conn]bool{},
	}
	s.mu.Lock()
	s.sessions[id] = ps
	s.mu.Unlock()
	go s.readLoop(ps)
	go s.waitLoop(ps, cmd)
	return Response{OK: true, Sessions: []Session{ps.snapshot()}}
}

func (s *Server) readLoop(ps *ptySession) {
	buf := make([]byte, 8192)
	for {
		n, err := ps.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			ps.ring = append(ps.ring, chunk...)
			if len(ps.ring) > maxRingBytes {
				ps.ring = append([]byte(nil), ps.ring[len(ps.ring)-maxRingBytes:]...)
			}
			clients := make([]net.Conn, 0, len(ps.clients))
			for c := range ps.clients {
				clients = append(clients, c)
			}
			s.mu.Unlock()
			for _, c := range clients {
				_, _ = c.Write(chunk)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) waitLoop(ps *ptySession, cmd *exec.Cmd) {
	err := cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	ps.Running = false
	ps.EndedAt = time.Now()
	if err != nil {
		ps.ExitError = err.Error()
	}
	for c := range ps.clients {
		_ = c.Close()
	}
	_ = ps.pty.Close()
}

func (s *Server) attach(conn net.Conn, req Request) {
	s.mu.Lock()
	ps := s.sessions[req.ID]
	if ps == nil {
		s.mu.Unlock()
		writeResponse(conn, Response{Error: "unknown session: " + req.ID})
		return
	}
	if req.Rows > 0 && req.Cols > 0 {
		_ = pty.Setsize(ps.pty, &pty.Winsize{Rows: req.Rows, Cols: req.Cols})
	}
	ring := append([]byte(nil), ps.ring...)
	ps.clients[conn] = true
	s.mu.Unlock()

	writeResponse(conn, Response{OK: true, Sessions: []Session{ps.snapshot()}})
	if len(ring) > 0 {
		_, _ = conn.Write(ring)
	}
	_, _ = io.Copy(ps.pty, conn)

	s.mu.Lock()
	delete(ps.clients, conn)
	s.mu.Unlock()
}

func (s *Server) resize(req Request) Response {
	s.mu.Lock()
	ps := s.sessions[req.ID]
	s.mu.Unlock()
	if ps == nil {
		return Response{Error: "unknown session: " + req.ID}
	}
	if req.Rows == 0 || req.Cols == 0 {
		return Response{Error: "rows and cols are required"}
	}
	if err := pty.Setsize(ps.pty, &pty.Winsize{Rows: req.Rows, Cols: req.Cols}); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{OK: true}
}

func (s *Server) list() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, ps := range s.sessions {
		out = append(out, ps.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (s *Server) kill(id string) Response {
	s.mu.Lock()
	ps := s.sessions[id]
	s.mu.Unlock()
	if ps == nil {
		return Response{Error: "unknown session: " + id}
	}
	if ps.Running && ps.PID > 0 {
		_ = syscall.Kill(ps.PID, syscall.SIGTERM)
	}
	return Response{OK: true}
}

func (s *Server) detach(id string) Response {
	s.mu.Lock()
	ps := s.sessions[id]
	if ps == nil {
		s.mu.Unlock()
		return Response{Error: "unknown session: " + id}
	}
	clients := make([]net.Conn, 0, len(ps.clients))
	for c := range ps.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
	return Response{OK: true}
}

func (ps *ptySession) snapshot() Session {
	return Session{ID: ps.ID, CWD: ps.CWD, Command: append([]string(nil), ps.Command...), PID: ps.PID, Running: ps.Running, ExitError: ps.ExitError, StartedAt: ps.StartedAt, EndedAt: ps.EndedAt, Clients: len(ps.clients)}
}

func writeResponse(w io.Writer, resp Response) {
	if !resp.OK && resp.Error == "" {
		resp.Error = "unknown error"
	}
	b, _ := json.Marshal(resp)
	_, _ = w.Write(append(b, '\n'))
}

func ClientRequest(req Request) (Response, error) {
	conn, reader, resp, err := clientRequestRaw(req)
	if conn != nil {
		_ = conn.Close()
	}
	_ = reader
	return resp, err
}

func clientRequestRaw(req Request) (net.Conn, *bufio.Reader, Response, error) {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		return nil, nil, Response{}, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		_ = conn.Close()
		return nil, nil, Response{}, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, nil, Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		_ = conn.Close()
		return nil, nil, Response{}, err
	}
	if !resp.OK {
		_ = conn.Close()
		return nil, nil, resp, errors.New(resp.Error)
	}
	return conn, reader, resp, nil
}

func EnsureDaemon() error {
	if _, err := ClientRequest(Request{Op: "ping"}); err == nil {
		return nil
	}
	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = os.Args[0]
	}
	cmd := exec.Command(bin, "ptyd")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if _, err := ClientRequest(Request{Op: "ping"}); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start: %w", last)
}

func Start(id, cwd string, command []string) (Response, error) {
	if err := EnsureDaemon(); err != nil {
		return Response{}, err
	}
	rows, cols := termSize()
	return ClientRequest(Request{Op: "start", ID: id, CWD: cwd, Command: command, Rows: rows, Cols: cols})
}

func List() (Response, error) {
	if err := EnsureDaemon(); err != nil {
		return Response{}, err
	}
	return ClientRequest(Request{Op: "list"})
}

func ListIfRunning() (Response, error) {
	return ClientRequest(Request{Op: "list"})
}

func Kill(id string) (Response, error) {
	if err := EnsureDaemon(); err != nil {
		return Response{}, err
	}
	return ClientRequest(Request{Op: "kill", ID: id})
}

func Detach(id string) (Response, error) {
	if err := EnsureDaemon(); err != nil {
		return Response{}, err
	}
	return ClientRequest(Request{Op: "detach", ID: id})
}

func Attach(id string) error {
	if err := EnsureDaemon(); err != nil {
		return err
	}
	rows, cols := termSize()
	conn, reader, _, err := clientRequestRaw(Request{Op: "attach", ID: id, Rows: rows, Cols: cols})
	if err != nil {
		return err
	}
	defer conn.Close()

	oldState, rawErr := term.MakeRaw(int(os.Stdin.Fd()))
	if rawErr == nil {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}
	stdin, err := cancelreader.NewReader(os.Stdin)
	if err != nil {
		return err
	}
	defer stdin.Close()

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, io.MultiReader(reader, conn))
		close(done)
	}()
	resizeStop := watchResize(id)
	defer resizeStop()
	detached := make(chan struct{})
	inputDone := make(chan struct{})
	go func() {
		copyInputWithDetach(conn, stdin, detached)
		close(inputDone)
	}()
	finish := func() error {
		_ = stdin.Cancel()
		select {
		case <-inputDone:
		case <-time.After(200 * time.Millisecond):
		}
		return nil
	}
	select {
	case <-done:
		return finish()
	case <-detached:
		_ = conn.Close()
		return finish()
	}
}

func watchResize(id string) func() {
	if runtime.GOOS == "windows" {
		return func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				rows, cols := termSize()
				_, _ = ClientRequest(Request{Op: "resize", ID: id, Rows: rows, Cols: cols})
			case <-stop:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(stop)
	}
}

func copyInputWithDetach(dst io.Writer, src io.Reader, detached chan<- struct{}) {
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if idx := bytes.IndexByte(chunk, 0x1d); idx >= 0 { // Ctrl-]
				if idx > 0 {
					_, _ = dst.Write(chunk[:idx])
				}
				close(detached)
				return
			}
			_, _ = dst.Write(chunk)
		}
		if err != nil {
			return
		}
	}
}

func agentEnv(id, cwd string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		switch key {
		case "TMUX", "TMUX_PANE", "PWD", "GLINT_WORKSPACE", "GLINT_PTY_SESSION":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "TERM=xterm-256color", "GLINT_PTY_SESSION="+id)
	if strings.TrimSpace(cwd) != "" {
		env = append(env, "PWD="+cwd, "GLINT_WORKSPACE="+cwd)
	}
	return env
}

func termSize() (uint16, uint16) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 24, 80
	}
	return uint16(h), uint16(w)
}

func cleanID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "glint://")
	id = strings.ReplaceAll(id, string(filepath.Separator), "-")
	return id
}

func max(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

func FormatSessions(sessions []Session) string {
	if len(sessions) == 0 {
		return "no pty sessions\n"
	}
	var b strings.Builder
	for _, s := range sessions {
		status := "exited"
		if s.Running {
			status = "running"
		}
		cmd := strings.Join(s.Command, " ")
		pid := "-"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		fmt.Fprintf(&b, "%s\t%s\tpid=%s\tclients=%d\t%s\t%s\n", s.ID, status, pid, s.Clients, s.CWD, cmd)
	}
	return b.String()
}
