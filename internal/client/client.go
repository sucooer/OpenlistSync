package client

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrComplete = errors.New("file already complete")
	ErrNotFound = errors.New("not found")
)

// FsObject mirrors the OpenList /api/fs/list & /api/fs/get item schema.
type FsObject struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Sign     string `json:"sign"`
	Type     int    `json:"type"`
	RawURL   string `json:"raw_url"`
}

func (o *FsObject) ModTime() (time.Time, bool) {
	if o.Modified == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, o.Modified)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

type fsListData struct {
	Content []FsObject `json:"content"`
	Total   int        `json:"total"`
	HasMore bool       `json:"has_more"`
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type Client struct {
	baseURL   string
	token     string
	username  string
	password  string
	proxyMode bool
	hc        *http.Client
	logf      func(format string, a ...any)

	mu        sync.Mutex
	loginOnce bool
}

func New(baseURL, token, username, password string, proxyMode bool, logf func(string, ...any)) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		username:  username,
		password:  password,
		proxyMode: proxyMode,
		logf:      logf,
		hc: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          20,
				MaxIdleConnsPerHost:   8,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ExpectContinueTimeout: 3 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
			},
		},
	}
}

func (c *Client) canLogin() bool { return c.username != "" && c.password != "" }

func (c *Client) loginLocked(ctx context.Context) error {
	sum := md5.Sum([]byte(c.password))
	body, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": hex.EncodeToString(sum[:]),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login/hash", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("login: decode response: %w", err)
	}
	if resp.StatusCode != 200 || env.Code != 200 {
		return fmt.Errorf("login failed: code=%d message=%s", env.Code, env.Message)
	}
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.Token == "" {
		return fmt.Errorf("login: no token in response")
	}
	c.token = data.Token
	c.loginOnce = true
	return nil
}

// ensureAuth returns an error if no token is available (and login is impossible).
func (c *Client) ensureAuth(ctx context.Context) error {
	if c.token != "" {
		return nil
	}
	if !c.canLogin() {
		return fmt.Errorf("no token configured and no username/password to log in with")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return nil
	}
	return c.loginLocked(ctx)
}

// doJSON performs an API call returning the OpenList envelope, retrying once after a fresh login on 401.
func (c *Client) doJSON(ctx context.Context, method, apiPath string, body any, out any) error {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyBytes = b
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureAuth(ctx); err != nil {
			return err
		}
		var rd io.Reader
		if bodyBytes != nil {
			rd = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+apiPath, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", c.token)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("%s %s: bad response: %w", method, apiPath, err)
		}
		if env.Code == 401 && attempt == 0 && c.canLogin() {
			c.invalidateToken()
			continue
		}
		if resp.StatusCode != 200 || env.Code != 200 {
			msg := env.Message
			if strings.Contains(strings.ToLower(msg), "storage not found") {
				msg += " (OpenList 无法将这个路径归到任何已挂载的存储根；检查连接配置或在 OpenList 后台确认 storage 列表里包含该路径)"
			}
			return fmt.Errorf("%s %s: code=%d message=%s", method, apiPath, env.Code, msg)
		}
		if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("%s %s: decode data: %w", method, apiPath, err)
			}
		}
		return nil
	}
	return fmt.Errorf("%s %s: auth retry exhausted", method, apiPath)
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.username != "" {
		c.token = ""
		c.loginOnce = false
	}
}

// ListAll returns every item in a directory (paginated).
func (c *Client) ListAll(ctx context.Context, remotePath string) ([]FsObject, error) {
	page := 1
	per := 200
	var items []FsObject
	for {
		var d fsListData
		err := c.doJSON(ctx, http.MethodPost, "/api/fs/list", map[string]any{
			"path":     remotePath,
			"password": "",
			"page":     page,
			"per_page": per,
			"refresh":  false,
		}, &d)
		if err != nil {
			return nil, err
		}
		items = append(items, d.Content...)
		if !d.HasMore || len(d.Content) == 0 || page*per >= d.Total {
			break
		}
		page++
		if page > 100000 {
			return nil, fmt.Errorf("list %s: pagination runaway", remotePath)
		}
	}
	return items, nil
}

// GetInfo returns fresh object info incl. sign/raw_url.
func (c *Client) GetInfo(ctx context.Context, remotePath string) (FsObject, error) {
	var o FsObject
	err := c.doJSON(ctx, http.MethodPost, "/api/fs/get", map[string]any{"path": remotePath}, &o)
	return o, err
}

func (c *Client) Mkdir(ctx context.Context, remotePath string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/fs/mkdir", map[string]any{"path": remotePath}, nil)
}

// Remove deletes several names inside dir in one call.
func (c *Client) Remove(ctx context.Context, dir string, names []string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/fs/remove", map[string]any{
		"dir":   dir,
		"names": names,
	}, nil)
}

// Upload streams src to remotePath via PUT /api/fs/put.
// localPath is only used to derive the Content-Type.
func (c *Client) Upload(ctx context.Context, localPath, remotePath string, overwrite bool, src io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/fs/put", src)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("File-Path", url.QueryEscape(remotePath))
	req.Header.Set("Content-Type", contentType(localPath))
	req.ContentLength = size
	req.Header.Set("As-Task", "false")
	req.Header.Set("Overwrite", strconv.FormatBool(overwrite))

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("upload %s: bad response: %w", remotePath, err)
	}
	if resp.StatusCode != 200 || env.Code != 200 {
		return fmt.Errorf("upload %s: code=%d message=%s", remotePath, env.Code, env.Message)
	}
	return nil
}

func contentType(file string) string {
	if ct := mime.TypeByExtension(path.Ext(file)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// Download holds an open download stream.
type Download struct {
	Body    io.ReadCloser
	Size    int64 // expected content length, -1 if unknown
	Resumed bool  // server honoured our Range request
}

// FileURL builds the direct (/d) or proxy (/p) download URL.
func (c *Client) FileURL(remotePath, sign string) string {
	prefix := "d"
	if c.proxyMode {
		prefix = "p"
	}
	u := c.baseURL + "/" + prefix + escapePath(remotePath)
	if sign != "" {
		u += "?sign=" + sign
	}
	return u
}

func escapePath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(segs, "/")
}

// OpenDownload starts a (possibly resumed) download of remotePath.
// offset > 0 requests a Range; if the server ignores it a full download restarts.
func (c *Client) OpenDownload(ctx context.Context, remotePath, sign string, offset int64) (*Download, error) {
	return c.openDownload(ctx, remotePath, sign, offset, 0)
}

func (c *Client) openDownload(ctx context.Context, remotePath, sign string, offset int64, attempt int) (*Download, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}
	u := c.FileURL(remotePath, sign)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return &Download{
			Body:    resp.Body,
			Size:    resp.ContentLength,
			Resumed: resp.StatusCode == http.StatusPartialContent,
		}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		resp.Body.Close()
		return nil, ErrComplete
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, remotePath)
	case http.StatusUnauthorized:
		resp.Body.Close()
		if attempt == 0 && c.canLogin() {
			c.invalidateToken()
			if err := c.ensureAuth(ctx); err != nil {
				return nil, err
			}
			return c.openDownload(ctx, remotePath, sign, offset, attempt+1)
		}
		if attempt <= 2 {
			// sign may be missing or stale; fetch fresh info and retry
			info, err := c.GetInfo(ctx, remotePath)
			if err == nil && info.Sign != "" {
				return c.openDownload(ctx, remotePath, info.Sign, offset, attempt+1)
			}
		}
		return nil, fmt.Errorf("download %s: unauthorized (is signing enabled?)", remotePath)
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: http %d", remotePath, resp.StatusCode)
	}
}