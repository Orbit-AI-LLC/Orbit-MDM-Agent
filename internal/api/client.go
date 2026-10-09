// Package api speaks Orbit RMM's agent API (/api/rmm/v1/), JSON over HTTPS.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrRetired means the server no longer manages this computer.
var ErrRetired = errors.New("this computer is no longer managed by Orbit RMM")

// ErrUnauthorized means the agent's secret was refused.
var ErrUnauthorized = errors.New("the server refused this agent's credentials")

// Client talks to one server as one agent.
type Client struct {
	Server  string
	Secret  string
	Version string
	HTTP    *http.Client
}

// New makes a client with sensible timeouts.
func New(server, secret, version string) *Client {
	return &Client{
		Server:  strings.TrimRight(server, "/"),
		Secret:  secret,
		Version: version,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Sealed is a task as the server sends it: signed bytes and their signature.
type Sealed struct {
	ID        string `json:"id"`
	Envelope  string `json:"envelope"`
	Signature string `json:"signature"`
}

// EnrollRequest is what the agent tells the server about the computer.
type EnrollRequest struct {
	Token        string `json:"token"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	OSVersion    string `json:"os_version"`
	Arch         string `json:"arch"`
	Serial       string `json:"serial"`
	MachineID    string `json:"machine_id"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Version      string `json:"version"`
}

// EnrollResponse carries the agent's identity and the key it must trust.
type EnrollResponse struct {
	AgentID        string `json:"agent_id"`
	Secret         string `json:"secret"`
	ServerKey      string `json:"server_key"`
	CheckinSeconds int    `json:"checkin_seconds"`
	PulseAddr      string `json:"pulse_addr"`
	Device         string `json:"device"`
}

// CheckinResponse is the server's answer to a check-in.
type CheckinResponse struct {
	Tasks          []Sealed `json:"tasks"`
	CheckinSeconds int      `json:"checkin_seconds"`
	Live           bool     `json:"live"`
	WatchServices  []string `json:"watch_services"`
	PulseAddr      string   `json:"pulse_addr"`
	ServerTime     int64    `json:"server_time"`
}

// WaitResponse is the answer to a long poll.
type WaitResponse struct {
	Tasks []Sealed `json:"tasks"`
	Live  bool     `json:"live"`
}

// Result is what a task produced.
type Result struct {
	ExitCode *int           `json:"exit_code"`
	Stdout   string         `json:"stdout"`
	Stderr   string         `json:"stderr"`
	Data     map[string]any `json:"data,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any, auth bool, timeout time.Duration) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Server+"/api/rmm/v1/"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "orbit-agent/"+c.Version)
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusGone:
		return ErrRetired
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode >= 400:
		var e apiError
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			return fmt.Errorf("%s (%d)", e.Message, resp.StatusCode)
		}
		return fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Enroll joins the organization the install token belongs to.
func (c *Client) Enroll(ctx context.Context, req EnrollRequest) (*EnrollResponse, error) {
	var out EnrollResponse
	if err := c.do(ctx, http.MethodPost, "enroll", req, &out, false, 60*time.Second); err != nil {
		return nil, err
	}
	return &out, nil
}

// Checkin reports health and collects tasks.
func (c *Client) Checkin(ctx context.Context, metrics map[string]any) (*CheckinResponse, error) {
	var out CheckinResponse
	err := c.do(ctx, http.MethodPost, "checkin", map[string]any{"version": c.Version, "metrics": metrics}, &out, true, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Wait holds a request open until there is work, while someone is watching.
func (c *Client) Wait(ctx context.Context) (*WaitResponse, error) {
	var out WaitResponse
	if err := c.do(ctx, http.MethodGet, "wait", nil, &out, true, 90*time.Second); err != nil {
		return nil, err
	}
	return &out, nil
}

// RegisterPulseKey tells the server this agent's pulse channel public key (base64).
func (c *Client) RegisterPulseKey(ctx context.Context, publicKey string) error {
	return c.do(ctx, http.MethodPost, "pulse_key", map[string]any{"pulse_key": publicKey}, nil, true, 30*time.Second)
}

// Start says a task has begun.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "tasks/"+id+"/start", map[string]any{}, nil, true, 30*time.Second)
}

// Report sends a task's result.
func (c *Client) Report(ctx context.Context, id string, result Result) error {
	return c.do(ctx, http.MethodPost, "tasks/"+id+"/result", result, nil, true, 120*time.Second)
}
