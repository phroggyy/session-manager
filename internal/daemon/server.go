package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"go.uber.org/zap"
)

// Request represents a JSON-RPC style request from a client.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     int             `json:"id"`
}

// Response represents a JSON-RPC style response to a client.
type Response struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
	ID     int         `json:"id"`
}

// Server handles Unix socket connections and dispatches requests to the daemon.
type Server struct {
	socketPath string
	listener   net.Listener
	daemon     *Daemon
	logger     *zap.Logger

	clients   map[net.Conn]*clientConn
	clientsMu sync.RWMutex
}

// clientConn tracks a connected client and its subscription state.
type clientConn struct {
	conn       net.Conn
	encoder    *json.Encoder
	subscribed bool
	eventCh    chan Event
	mu         sync.Mutex
}

// NewServer creates a new Unix socket server.
func NewServer(socketPath string, daemon *Daemon, logger *zap.Logger) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Server{
		socketPath: socketPath,
		daemon:     daemon,
		logger:     logger,
		clients:    make(map[net.Conn]*clientConn),
	}
}

// Start begins listening for connections on the Unix socket.
func (s *Server) Start() error {
	// Remove existing socket file if it exists
	if err := os.RemoveAll(s.socketPath); err != nil {
		return fmt.Errorf("failed to remove existing socket: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	s.listener = listener
	s.logger.Info("server listening", zap.String("socket", s.socketPath))

	return nil
}

// Serve accepts and handles connections. This method blocks until the context
// is cancelled or an error occurs.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		return fmt.Errorf("server not started")
	}

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Check if we're shutting down
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				s.logger.Error("failed to accept connection", zap.Error(err))
				continue
			}
		}

		s.logger.Debug("client connected", zap.String("remote", conn.RemoteAddr().String()))
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection processes requests from a single client connection.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	client := &clientConn{
		conn:    conn,
		encoder: json.NewEncoder(conn),
		eventCh: make(chan Event, 256),
	}

	s.clientsMu.Lock()
	s.clients[conn] = client
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()

		// Unsubscribe if subscribed
		if client.subscribed {
			close(client.eventCh)
		}
	}()

	decoder := json.NewDecoder(conn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var req Request
		if err := decoder.Decode(&req); err != nil {
			if err.Error() != "EOF" {
				s.logger.Debug("failed to decode request", zap.Error(err))
			}
			return
		}

		s.logger.Debug("received request",
			zap.String("method", req.Method),
			zap.Int("id", req.ID),
		)

		resp := s.handleRequest(ctx, client, &req)
		resp.ID = req.ID

		client.mu.Lock()
		err := client.encoder.Encode(resp)
		client.mu.Unlock()

		if err != nil {
			s.logger.Debug("failed to send response", zap.Error(err))
			return
		}
	}
}

// handleRequest dispatches a request to the appropriate handler.
func (s *Server) handleRequest(ctx context.Context, client *clientConn, req *Request) Response {
	switch req.Method {
	case "switch":
		return s.handleSwitch(req)
	case "stop":
		return s.handleStop()
	case "status":
		return s.handleStatus()
	case "subscribe":
		return s.handleSubscribe(ctx, client)
	default:
		return Response{Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}

// SwitchParams contains the parameters for a switch request.
type SwitchParams struct {
	Target string `json:"target"`
}

// handleSwitch handles a request to switch worktrees.
func (s *Server) handleSwitch(req *Request) Response {
	var params SwitchParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return Response{Error: fmt.Sprintf("invalid params: %v", err)}
	}

	if params.Target == "" {
		return Response{Error: "target is required"}
	}

	if err := s.daemon.Switch(params.Target); err != nil {
		return Response{Error: err.Error()}
	}

	return Response{Result: map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("switched to %s", params.Target),
	}}
}

// handleStop handles a request to stop the daemon.
func (s *Server) handleStop() Response {
	if err := s.daemon.Stop(); err != nil {
		return Response{Error: err.Error()}
	}

	return Response{Result: map[string]interface{}{
		"success": true,
		"message": "daemon stopped",
	}}
}

// handleStatus handles a request for daemon status.
func (s *Server) handleStatus() Response {
	status := s.daemon.Status()
	return Response{Result: status}
}

// handleSubscribe handles a request to subscribe to events.
func (s *Server) handleSubscribe(ctx context.Context, client *clientConn) Response {
	if client.subscribed {
		return Response{Result: map[string]interface{}{
			"success": true,
			"message": "already subscribed",
		}}
	}

	client.subscribed = true
	s.daemon.addSubscriber(client.eventCh)

	// Start goroutine to send events to this client
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-client.eventCh:
				if !ok {
					return
				}

				// Send event as a response with ID 0 (unsolicited)
				eventResp := Response{
					Result: event,
					ID:     0,
				}

				client.mu.Lock()
				err := client.encoder.Encode(eventResp)
				client.mu.Unlock()

				if err != nil {
					s.logger.Debug("failed to send event", zap.Error(err))
					return
				}
			}
		}
	}()

	return Response{Result: map[string]interface{}{
		"success": true,
		"message": "subscribed to events",
	}}
}

// BroadcastEvent sends an event to all subscribed clients.
func (s *Server) BroadcastEvent(event Event) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for _, client := range s.clients {
		if client.subscribed {
			select {
			case client.eventCh <- event:
			default:
				// Drop if buffer is full
				s.logger.Debug("dropping event for slow client")
			}
		}
	}
}

// Stop closes the server and all client connections.
func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// SocketPath returns the path to the Unix socket.
func (s *Server) SocketPath() string {
	return s.socketPath
}
