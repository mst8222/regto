// Package regto 是 Regto 的 Go 客户端 SDK（SPEC-SDK-001）。
//
// 设计取向（详见 DESIGN-SDK-001）：
//   - 只依赖标准库：SDK 不该把服务端的领域类型带给调用方，也不该引入第三方依赖。
//   - 与 HTTP 契约一一对应：字段与 api/openapi/v1.yaml 对齐，不做"贴心"的隐式转换。
//   - 错误可判定：服务端返回 RFC 7807 problem+json，SDK 把它映射成可 errors.Is/As 的错误，
//     调用方能用 StatusCode/Code 分别判 HTTP 状态与业务码。
//
// 用法：
//
//	c, _ := regto.New(regto.WithBaseURL("http://127.0.0.1:8080"), regto.WithToken(jwt))
//	stories, page, err := c.Stories().List(ctx, regto.StoryQuery{ProductID: 1})
package regto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeout 是默认单次请求超时。SDK 不做重试——重试语义（幂等性）由调用方决定，
// 静默重试会把非幂等的写操作变成重复提交。
const DefaultTimeout = 30 * time.Second

// Client 是 Regto 的 HTTP 客户端。零值不可用，请用 New 构造。
type Client struct {
	baseURL    string
	token      string
	apiKey     string
	httpClient *http.Client
}

// Option 是构造 Client 的可选参数。
type Option func(*Client)

// WithBaseURL 指定服务地址（例如 http://127.0.0.1:8080）。末尾斜杠会被去掉。
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithToken 指定 JWT（登录接口换取），会带在 Authorization: Bearer 上。
func WithToken(t string) Option {
	return func(c *Client) { c.token = t }
}

// WithAPIKey 指定 API Key，会带在 X-Regto-Api-Key 上。
// 与 WithToken 可以同时给：服务端按「API Key 优先」的顺序识别主体。
func WithAPIKey(k string) Option {
	return func(c *Client) { c.apiKey = k }
}

// WithHTTPClient 替换底层 HTTP 客户端（自定义超时、代理、连接池时使用）。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// New 构造客户端。baseURL 为空是错误——静默默认到 localhost 会让调用方
// 在配错环境时"连上了但连的不是它想连的那台"。
func New(opts ...Option) (*Client, error) {
	c := &Client{httpClient: &http.Client{Timeout: DefaultTimeout}}
	for _, o := range opts {
		o(c)
	}
	if c.baseURL == "" {
		return nil, errors.New("regto: 缺少 baseURL（用 WithBaseURL 指定）")
	}
	if _, err := url.ParseRequestURI(c.baseURL); err != nil {
		return nil, fmt.Errorf("regto: baseURL 非法: %w", err)
	}
	return c, nil
}

// Stories 返回需求资源。
func (c *Client) Stories() *StoryService { return &StoryService{c: c} }

// Cases 返回用例资源。
func (c *Client) Cases() *CaseService { return &CaseService{c: c} }

// Reports 返回报表资源。
func (c *Client) Reports() *ReportService { return &ReportService{c: c} }

// Audit 返回审计报表资源（SPEC-AUDIT-001）。
func (c *Client) Audit() *AuditService { return &AuditService{c: c} }

// Error 是 SDK 的统一错误：既有 HTTP 状态码，也有服务端的业务错误码。
//
// 为什么要两层：HTTP 状态只够判"大类"（401 未认证 / 422 规则拒绝），
// 但同一个 422 下可能有 INVALID_TRANSITION、IMMUTABLE 等完全不同的原因，
// 调用方要能据 Code 分支处理。
type Error struct {
	StatusCode int    `json:"status"`
	Code       string `json:"code"`
	Detail     string `json:"detail"`
	RequestID  string `json:"request_id,omitempty"`
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("regto: HTTP %d: %s", e.StatusCode, e.Detail)
	}
	return fmt.Sprintf("regto: HTTP %d [%s]: %s", e.StatusCode, e.Code, e.Detail)
}

// Is 让调用方可以用 errors.Is(err, ErrXxx) 判大类，不必解析字符串。
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthenticated:
		return e.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrConflict:
		return e.StatusCode == http.StatusConflict
	case ErrInvalidArgument:
		return e.StatusCode == http.StatusBadRequest
	case ErrRuleRejected:
		return e.StatusCode == http.StatusUnprocessableEntity
	}
	return false
}

// 错误大类哨兵：与 errors.Is 搭配使用。
var (
	ErrUnauthenticated = errors.New("regto: 未认证")
	ErrForbidden       = errors.New("regto: 无权限")
	ErrNotFound        = errors.New("regto: 资源不存在")
	ErrConflict        = errors.New("regto: 状态冲突")
	ErrInvalidArgument = errors.New("regto: 请求参数非法")
	ErrRuleRejected    = errors.New("regto: 业务规则拒绝")
)

// problem 是服务端 problem+json 的解码结构（SDK 只取需要的字段）。
type problem struct {
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
	Status    int    `json:"status"`
}

// do 发一个请求并把 2xx 的响应体解到 out。
//
// 约定：
//   - 非 2xx 一律解析 problem+json；解析不出来时用状态码兜底，绝不返回 nil error。
//   - 2xx 但 out 为 nil 时不解码（例如只需要状态码的调用）。
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("regto: 序列化请求体失败: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("regto: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.apiKey != "" {
		req.Header.Set("X-Regto-Api-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("regto: 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("regto: 解析响应失败（HTTP %d）: %w", resp.StatusCode, err)
	}
	return nil
}

// decodeError 把非 2xx 响应转成 *Error。
func decodeError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	e := &Error{StatusCode: resp.StatusCode, Detail: strings.TrimSpace(string(raw))}
	var p problem
	if err := json.Unmarshal(raw, &p); err == nil {
		if p.Detail != "" {
			e.Detail = p.Detail
		}
		e.Code = p.Code
		e.RequestID = p.RequestID
	}
	if e.Detail == "" {
		e.Detail = http.StatusText(resp.StatusCode)
	}
	return e
}

// Page 是分页信息（与服务端 shared.Page 对齐）。
type Page struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
	HasMore bool  `json:"has_more"`
}

// apiPath 拼 /api/v1 前缀的路径。
func apiPath(p string) string { return "/api/v1" + p }

// queryInt 把非零整数放进 query（零值省略，让服务端用自己的默认值）。
func queryInt(q url.Values, key string, v int64) {
	if v != 0 {
		q.Set(key, strconv.FormatInt(v, 10))
	}
}

// queryString 把非空字符串放进 query。
func queryString(q url.Values, key, v string) {
	if v != "" {
		q.Set(key, v)
	}
}
