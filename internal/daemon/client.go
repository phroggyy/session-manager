package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// StatusResponse contains the current status of the daemon.
type StatusResponse struct {
	SessionID       string          `json:"session_id"`
	RepoPath        string          `json:"repo_path"`
	CurrentWorktree string          `json:"current_worktree"`
	CurrentBranch   string          `json:"current_branch"`
	Processes       []ProcessStatus `json:"processes"`
	Running         bool            `json:"running"`
}

// ProcessStatus contains the status of a single process.
type ProcessStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

// Client communicates with the daemon over a Unix socket.
type Client struct {
	conn      net.Conn
	encoder   *json.Encoder
	requestID int32

	// Single reader goroutine dispatches to these
	eventCh    chan Event
	responseCh chan Response
	readerDone chan struct{}

	mu       sync.Mutex
	closed   bool
	listening bool
}

// Connect establishes a connection to the daemon.
func Connect(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	c := &Client{
		conn:       conn,
		encoder:    json.NewEncoder(conn),
		eventCh:    make(chan Event, 256),
		responseCh: make(chan Response, 16),
		readerDone: make(chan struct{}),
	}

	// Start single reader goroutine
	go c.readLoop()

	return c, nil
}

// readLoop is the single goroutine that reads all messages from the connection.
func (c *Client) readLoop() {
	defer close(c.readerDone)
	defer close(c.eventCh)
	defer close(c.responseCh)

	decoder := json.NewDecoder(c.conn)

	for {
		var resp Response
		if err := decoder.Decode(&resp); err != nil {
			// Connection closed or error
			return
		}

		if resp.ID == 0 {
			// This is an event (unsolicited message)
			if resp.Result != nil {
				if eventData, ok := resp.Result.(map[string]any); ok {
					event := Event{}
					if t, ok := eventData["type"].(string); ok {
						event.Type = EventType(t)
					}
					if p, ok := eventData["process"].(string); ok {
						event.Process = p
					}
					event.Data = eventData["data"]

					select {
					case c.eventCh <- event:
					default:
						// Drop if buffer is full
					}
				}
			}
		} else {
			// This is a response to a request
			select {
			case c.responseCh <- resp:
			default:
				// Drop if buffer is full (shouldn't happen)
			}
		}
	}
}

// sendRequest sends a request and waits for a response.
func (c *Client) sendRequest(method string, params any) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("client is closed")
	}

	id := int(atomic.AddInt32(&c.requestID, 1))

	var paramsJSON json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		paramsJSON = data
	}

	req := Request{
		Method: method,
		Params: paramsJSON,
		ID:     id,
	}

	if err := c.encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response with matching ID
	for {
		select {
		case resp, ok := <-c.responseCh:
			if !ok {
				return nil, fmt.Errorf("connection closed")
			}
			if resp.ID == id {
				return &resp, nil
			}
			// Wrong ID, put it back (shouldn't happen with single client)
			// Just ignore for now
		case <-c.readerDone:
			return nil, fmt.Errorf("connection closed")
		}
	}
}

// Switch requests the daemon to switch to a different worktree.
func (c *Client) Switch(target string) error {
	params := SwitchParams{Target: target}
	resp, err := c.sendRequest("switch", params)
	if err != nil {
		return err
	}

	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	return nil
}

// Stop requests the daemon to stop all processes and shut down.
func (c *Client) Stop() error {
	resp, err := c.sendRequest("stop", nil)
	if err != nil {
		return err
	}

	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	return nil
}

// Status requests the current status of the daemon.
func (c *Client) Status() (*StatusResponse, error) {
	resp, err := c.sendRequest("status", nil)
	if err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	// Convert the result to StatusResponse
	data, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	var status StatusResponse
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal status: %w", err)
	}

	return &status, nil
}

// Subscribe requests to receive events from the daemon.
// Returns the event channel. Events are automatically received after Connect().
func (c *Client) Subscribe() (<-chan Event, error) {
	c.mu.Lock()
	if c.listening {
		c.mu.Unlock()
		return c.eventCh, nil
	}
	c.mu.Unlock()

	resp, err := c.sendRequest("subscribe", nil)
	if err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	c.mu.Lock()
	c.listening = true
	c.mu.Unlock()

	return c.eventCh, nil
}

// Events returns the event channel for receiving daemon events.
// Call Subscribe() first to start receiving events.
func (c *Client) Events() <-chan Event {
	return c.eventCh
}

// Close closes the connection to the daemon.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	return c.conn.Close()
}
