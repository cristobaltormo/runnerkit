package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type apiClient struct {
	base       string
	authHeader string
	authValue  string
	http       *http.Client
}

type httpError struct {
	Status  int
	Path    string
	Message string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("the server answered %d for %s: %s", e.Status, e.Path, e.Message)
}

func (c *apiClient) do(method, path string, form url.Values, out any) error {
	client := sameHostOnly(c.http)
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.base, "/")+path, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	if c.authValue != "" {
		req.Header.Set(c.authHeader, c.authValue)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return &httpError{Status: resp.StatusCode, Path: path, Message: message(data)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func sameHostOnly(base *http.Client) *http.Client {
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		first := via[0].URL
		if req.URL.Host != first.Host || req.URL.Scheme != first.Scheme {
			return fmt.Errorf("the server redirected to %s://%s; use the final address of the server so the token is not sent anywhere else", req.URL.Scheme, req.URL.Host)
		}
		return nil
	}
	return client
}

func message(data []byte) string {
	var m struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &m) == nil {
		if m.Message != "" {
			return m.Message
		}
		if m.Error != "" {
			return m.Error
		}
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

func get(c *http.Client, rawURL string) ([]byte, error) {
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func sumFromLine(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields[0]) != 64 {
			continue
		}
		if len(fields) == 1 || strings.TrimPrefix(fields[len(fields)-1], "*") == name || strings.HasSuffix(fields[len(fields)-1], "/"+name) {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}
