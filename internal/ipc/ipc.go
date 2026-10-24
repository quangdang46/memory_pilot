// Package ipc: Unix-socket JSON server with peer-credential checks and a
// closed op set + versioned envelope (ADR-007).
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const ProtocolVersion = 1

// Closed op set (ADR-007): unknown ops are rejected, never extended at runtime.
var Ops = map[string]bool{
	"status": true, "report": true, "explain": true, "doctor": true,
	"policy": true, "forecast": true,
	// Managed-launch registration (ADR-014): the wrapper tells the daemon
	// which process it spawned, whether that launch is stop-authorized, and
	// when it exits. Without these ops the owned-workload registry can never
	// be populated, so the L2 path has no legitimate target.
	"admit": true, "admit_exit": true,
}

type Request struct {
	V   int             `json:"v"` // protocol version
	Op  string          `json:"op"`
	Arg json.RawMessage `json:"arg,omitempty"`
}

type Response struct {
	V     int             `json:"v"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

type Handler interface {
	Handle(ctx context.Context, req Request) (Response, error)
}

type Server struct {
	path string
	ln   net.Listener
	mu   sync.Mutex
}

func Serve(ctx context.Context, socketPath string, h Handler) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	s := &Server{path: socketPath, ln: ln}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go s.acceptLoop(ctx, h)
	return s, nil
}

func (s *Server) acceptLoop(ctx context.Context, h Handler) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(ctx, conn, h)
	}
}

// peerUID checks the connecting peer (peer credential check).
func peerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1, err
	}
	var uid int
	var ctlErr error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			ctlErr = err
			return
		}
		uid = int(cred.Uid)
	})
	if err != nil || ctlErr != nil {
		return -1, fmt.Errorf("peercred: %v/%v", err, ctlErr)
	}
	return uid, nil
}

// peerPID returns the connecting process's pid. The daemon needs it to record
// which process asked for admission: without it, a workload launched through
// `mp run` can never be registered as daemon-owned, and the L2 path
// (ADR-014) has no legitimate target — the whole managed-workload design
// depends on this one number.
func peerPID(conn *net.UnixConn) (int32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int32
	var ctlErr error
	err = raw.Control(func(fd uintptr) {
		p, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if err != nil {
			ctlErr = err
			return
		}
		pid = int32(p)
	})
	if err != nil {
		return 0, err
	}
	return pid, ctlErr
}

// PeerPID exposes the peer pid to handlers through the request context.
type peerPIDKey struct{}

func WithPeerPID(ctx context.Context, pid int32) context.Context {
	return context.WithValue(ctx, peerPIDKey{}, pid)
}

func PeerPIDFrom(ctx context.Context) (int32, bool) {
	pid, ok := ctx.Value(peerPIDKey{}).(int32)
	return pid, ok
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return
	}
	if uid, err := peerUID(uc); err != nil || uid != os.Getuid() {
		writeResp(conn, Response{V: ProtocolVersion, Error: "peer credential check failed"})
		return
	}
	if pid, err := peerPID(uc); err == nil && pid > 0 {
		ctx = WithPeerPID(ctx, pid)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	if !sc.Scan() {
		return
	}
	var req Request
	if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
		writeResp(conn, Response{V: ProtocolVersion, Error: "bad request envelope"})
		return
	}
	if req.V != ProtocolVersion {
		writeResp(conn, Response{V: ProtocolVersion, Error: fmt.Sprintf("version mismatch: server v%d", ProtocolVersion)})
		return
	}
	if !Ops[req.Op] {
		writeResp(conn, Response{V: ProtocolVersion, Error: "unknown op"})
		return
	}
	resp, err := h.Handle(ctx, req)
	if err != nil {
		writeResp(conn, Response{V: ProtocolVersion, Error: err.Error()})
		return
	}
	writeResp(conn, resp)
}

func writeResp(conn net.Conn, r Response) {
	b, _ := json.Marshal(r)
	conn.Write(append(b, '\n'))
}

// Call is the client side (CLI uses this).
func Call(socketPath string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (s *Server) Path() string { return s.path }
