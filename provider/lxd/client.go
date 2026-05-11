package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	http       *http.Client
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 300 * time.Second,
		},
	}
}

func (c *Client) url(path string) string {
	return fmt.Sprintf("http://unix%s", path)
}

func (c *Client) Launch(ctx context.Context, name, cpu, memory, image string) error {
	alias := image
	if strings.Contains(image, ":") {
		parts := strings.SplitN(image, ":", 2)
		alias = parts[1]
	}
	body := map[string]interface{}{
		"name": name,
		"source": map[string]string{
			"type":  "image",
			"alias": alias,
		},
		"config": map[string]string{
			"limits.cpu":          cpu,
			"limits.memory":       memory,
			"security.nesting":    "true",
			"security.privileged": "true",
		},
	}
	if err := c.post(ctx, "/1.0/containers", body); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return c.Start(ctx, name)
}

func (c *Client) Start(ctx context.Context, name string) error {
	body := map[string]interface{}{
		"action":  "start",
		"timeout": 60,
	}
	return c.put(ctx, fmt.Sprintf("/1.0/containers/%s/state", name), body)
}

func (c *Client) Stop(ctx context.Context, name string) error {
	body := map[string]interface{}{
		"action":  "stop",
		"timeout": 30,
		"force":   true,
	}
	err := c.put(ctx, fmt.Sprintf("/1.0/containers/%s/state", name), body)
	if c.isNotFoundErr(err) {
		return nil
	}
	return err
}

func (c *Client) Exec(ctx context.Context, name, command string) error {
	return c.exec(ctx, name, command, false)
}

func (c *Client) ExecOutput(ctx context.Context, name, command string) (string, error) {
	err := c.exec(ctx, name, command, true)
	return "", err
}

func (c *Client) exec(ctx context.Context, name, command string, recordOutput bool) error {
	body := map[string]interface{}{
		"command":     []string{"bash", "-c", command},
		"environment": map[string]string{},
		"wait-for-websocket": false,
		"interactive":        false,
	}
	if recordOutput {
		body["record-output"] = true
	}

	var md execMetadata
	if err := c.postWithMetadata(ctx, fmt.Sprintf("/1.0/containers/%s/exec", name), body, &md); err != nil {
		return fmt.Errorf("exec request failed: %w", err)
	}
	if md.Return != 0 {
		return fmt.Errorf("command exited with code %d", md.Return)
	}
	return nil
}

type execMetadata struct {
	Return int `json:"return"`
}

func (c *Client) Delete(ctx context.Context, name string) error {
	err := c.delete(ctx, fmt.Sprintf("/1.0/containers/%s", name))
	if c.isNotFoundErr(err) {
		return nil
	}
	return err
}

func (c *Client) Info(ctx context.Context, name string) (string, error) {
	return c.get(ctx, fmt.Sprintf("/1.0/containers/%s", name))
}

func (c *Client) List(ctx context.Context) (string, error) {
	return c.get(ctx, "/1.0/containers")
}

type lxdResponse struct {
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Metadata   json.RawMessage `json:"metadata"`
	Error      string          `json:"error"`
	ErrorCode  int             `json:"error_code"`
	Operation  string          `json:"operation"`
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	return resp, nil
}

func (c *Client) post(ctx context.Context, path string, body interface{}) error {
	resp, err := c.doRequest(ctx, "POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var lxdResp lxdResponse
	if err := json.NewDecoder(resp.Body).Decode(&lxdResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if lxdResp.StatusCode != 200 && lxdResp.StatusCode != 100 {
		return fmt.Errorf("LXD API error (%d): %s", lxdResp.StatusCode, lxdResp.Error)
	}
	if lxdResp.Operation != "" {
		return c.waitOperation(ctx, lxdResp.Operation, nil)
	}
	return nil
}

func (c *Client) postWithMetadata(ctx context.Context, path string, body interface{}, md interface{}) error {
	resp, err := c.doRequest(ctx, "POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var lxdResp lxdResponse
	if err := json.NewDecoder(resp.Body).Decode(&lxdResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if lxdResp.StatusCode != 200 && lxdResp.StatusCode != 100 {
		return fmt.Errorf("LXD API error (%d): %s", lxdResp.StatusCode, lxdResp.Error)
	}
	if lxdResp.Operation != "" {
		return c.waitOperation(ctx, lxdResp.Operation, md)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string) (string, error) {
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

func (c *Client) put(ctx context.Context, path string, body interface{}) error {
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var lxdResp lxdResponse
	if err := json.NewDecoder(resp.Body).Decode(&lxdResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if lxdResp.StatusCode != 200 && lxdResp.StatusCode != 100 {
		return fmt.Errorf("LXD API error (%d): %s", lxdResp.StatusCode, lxdResp.Error)
	}
	if lxdResp.Operation != "" {
		return c.waitOperation(ctx, lxdResp.Operation, nil)
	}
	return nil
}

func (c *Client) delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.url(path), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	var lxdResp lxdResponse
	if err := json.NewDecoder(resp.Body).Decode(&lxdResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if lxdResp.StatusCode != 200 && lxdResp.StatusCode != 100 {
		return fmt.Errorf("LXD API error (%d): %s", lxdResp.StatusCode, lxdResp.Error)
	}
	if lxdResp.Operation != "" {
		return c.waitOperation(ctx, lxdResp.Operation, nil)
	}
	return nil
}

func (c *Client) isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "Not found")
}

func (c *Client) waitOperation(ctx context.Context, operationURL string, outMD interface{}) error {
	waitURL := strings.TrimSuffix(operationURL, "/") + "/wait"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, "GET", c.url(waitURL), nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		var lxdResp lxdResponse
		if err := json.NewDecoder(resp.Body).Decode(&lxdResp); err != nil {
			resp.Body.Close()
			return err
		}
		resp.Body.Close()

		if lxdResp.StatusCode == 200 {
			if outMD != nil && lxdResp.Metadata != nil {
				if err := json.Unmarshal(lxdResp.Metadata, outMD); err != nil {
					return fmt.Errorf("parse operation metadata: %w", err)
				}
			}
			return nil
		}
		if lxdResp.StatusCode >= 400 {
			return fmt.Errorf("operation failed: %s", lxdResp.Error)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
