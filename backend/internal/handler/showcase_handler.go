package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

const (
	// showcaseMaxBodyBytes 上游响应体读取上限（作品正文为单页 HTML，正常几十 KB）。
	showcaseMaxBodyBytes int64 = 4 * 1024 * 1024
	// showcaseUpstreamTimeout 上游只读接口的请求超时。
	showcaseUpstreamTimeout = 15 * time.Second
)

// ShowcaseHandler 代理「醍醐测试」只读作品展示接口（pelican-showcase）。
//
// 上游 API Key 只保存在服务端配置中，浏览器侧仅与本站后端通信；
// 接口挂在用户 JWT 认证组下，登录用户才可读取。仅支持 GET，
// 不发起测试、不调用模型、不影响 API Key 余额。
type ShowcaseHandler struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewShowcaseHandler 创建作品展示代理 handler。
// 未配置 showcase.base_url / showcase.api_key 时接口返回 503 提示未启用。
func NewShowcaseHandler(cfg *config.Config) *ShowcaseHandler {
	h := &ShowcaseHandler{
		client: &http.Client{Timeout: showcaseUpstreamTimeout},
	}
	if cfg != nil {
		h.baseURL = strings.TrimRight(cfg.Showcase.BaseURL, "/")
		h.apiKey = strings.TrimSpace(cfg.Showcase.APIKey)
	}
	return h
}

// configured 判断 showcase 是否已启用（base_url 与 api_key 均非空）。
func (h *ShowcaseHandler) configured() bool {
	return h.baseURL != "" && h.apiKey != ""
}

// upstream envelope（与上游 /api/v1/public/pelican-showcase 一致）。
type showcaseUpstreamEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// fetch 向上游发起带鉴权的 GET 请求，并透传 If-None-Match 条件请求头。
// 返回上游状态码、响应体与 ETag。
func (h *ShowcaseHandler) fetch(c *gin.Context, path string) (int, []byte, string, error) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		return 0, nil, "", fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Accept", "application/json")
	if inm := c.GetHeader("If-None-Match"); inm != "" {
		req.Header.Set("If-None-Match", inm)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, showcaseMaxBodyBytes+1))
	if err != nil {
		return resp.StatusCode, nil, "", fmt.Errorf("read upstream body: %w", err)
	}
	if int64(len(body)) > showcaseMaxBodyBytes {
		return http.StatusBadGateway, nil, "", fmt.Errorf("upstream response too large")
	}
	return resp.StatusCode, body, resp.Header.Get("ETag"), nil
}

// proxyGet 执行一次代理请求并把上游数据转换为站点标准响应。
// 上游 304 原样透传（含 ETag），让调用端可以复用 If-None-Match 缓存。
func (h *ShowcaseHandler) proxyGet(c *gin.Context, path string) {
	if !h.configured() {
		response.Error(c, http.StatusServiceUnavailable, "Showcase not configured")
		return
	}

	status, body, etag, err := h.fetch(c, path)
	if err != nil {
		response.InternalError(c, "Showcase upstream unavailable")
		return
	}

	if etag != "" {
		c.Header("ETag", etag)
	}
	if status == http.StatusNotModified {
		c.Status(http.StatusNotModified)
		return
	}

	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "60")
		response.Error(c, status, "Showcase upstream rate limited, retry later")
		return
	}
	if status != http.StatusOK {
		response.Error(c, http.StatusBadGateway, "Showcase upstream error")
		return
	}

	var envelope showcaseUpstreamEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != 0 {
		response.Error(c, http.StatusBadGateway, "Showcase upstream returned invalid payload")
		return
	}
	response.Success(c, envelope.Data)
}

// List 返回作品清单（分组 + 各作品摘要）。
// GET /api/v1/showcase
func (h *ShowcaseHandler) List(c *gin.Context) {
	h.proxyGet(c, "/api/v1/public/pelican-showcase")
}

// GetItem 返回单个作品的完整正文（response_text 为自包含 HTML）。
// GET /api/v1/showcase/items/:id
func (h *ShowcaseHandler) GetItem(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid showcase item id")
		return
	}
	h.proxyGet(c, fmt.Sprintf("/api/v1/public/pelican-showcase/items/%d", id))
}
