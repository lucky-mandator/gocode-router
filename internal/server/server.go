package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"gocode-router/internal/config"
	"gocode-router/internal/models"
	"gocode-router/internal/provider"
	"gocode-router/internal/router"
	"gocode-router/internal/translator"
)

const (
	maxBodyBytes        = 1 << 20 // 1 MiB
	shutdownGracePeriod = 10 * time.Second
	readTimeout         = 30 * time.Second
	writeTimeout        = 45 * time.Second
	idleTimeout         = 120 * time.Second
)

type Server struct {
	cfg     config.Config
	router  *router.Router
	app     *echo.Echo
	address string
}

// New constructs an HTTP server wired with routing and middleware.
func New(cfg config.Config, rt *router.Router) (*Server, error) {
	if rt == nil {
		return nil, errors.New("router must not be nil")
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = openAIErrorHandler

	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogLatency: true,
		LogMethod:  true,
		LogURI:     true,
		LogStatus:  true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("request",
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
				"latency_ms", v.Latency.Milliseconds(),
				"error", v.Error,
			)
			return nil
		},
	}))
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "DENY",
		HSTSMaxAge:            31536000,
		ContentSecurityPolicy: "default-src 'none'; frame-ancestors 'none'; form-action 'none'",
	}))

	srv := &Server{
		cfg:     cfg,
		router:  rt,
		app:     e,
		address: fmt.Sprintf(":%d", cfg.Server.Port),
	}

	srv.registerRoutes()

	return srv, nil
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	printStartupBanner(s.cfg.Server.Port)
	slog.Info("starting server", "addr", s.address)

	httpServer := &http.Server{
		Addr:         s.address,
		Handler:      s.app,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.app.StartServer(httpServer); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
		defer cancel()
		if err := s.app.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		slog.Info("server shutdown complete")
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) registerRoutes() {
	s.app.GET("/health", s.handleHealth)
	s.app.POST("/v1/chat/completions", s.handleChatCompletions)
	s.app.POST("/v1/completions", s.handleCompletions)
	s.app.POST("/v1/messages", s.handleClaudeMessages)
}

func (s *Server) handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleChatCompletions(c echo.Context) error {
	var req translator.ChatCompletionRequest
	if err := decodeRequestBody(c, &req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	unifiedReq := req.ToUnified()

	resp, modelInfo, err := s.router.Chat(ctx, unifiedReq)
	if err != nil {
		return toHTTPError(err)
	}
	if resp == nil {
		return requestError{
			Status:  http.StatusBadGateway,
			Message: "upstream provider returned an empty response",
			Type:    "upstream_error",
		}
	}

	openAIResp := translator.FromUnifiedChat(modelInfo.ID, time.Now().Unix(), resp)
	return c.JSON(http.StatusOK, openAIResp)
}

func (s *Server) handleCompletions(c echo.Context) error {
	var req translator.CompletionRequest
	if err := decodeRequestBody(c, &req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	unifiedReq := req.ToUnified()

	resp, modelInfo, err := s.router.Completion(ctx, unifiedReq)
	if err != nil {
		return toHTTPError(err)
	}
	if resp == nil {
		return requestError{
			Status:  http.StatusBadGateway,
			Message: "upstream provider returned an empty response",
			Type:    "upstream_error",
		}
	}

	openAIResp := translator.FromUnifiedCompletion(modelInfo.ID, time.Now().Unix(), resp)
	return c.JSON(http.StatusOK, openAIResp)
}

func (s *Server) handleClaudeMessages(c echo.Context) error {
	var req translator.ClaudeMessageRequest
	if err := decodeRequestBody(c, &req); err != nil {
		return err
	}

	ctx := c.Request().Context()
	requestedStream := req.Stream
	unifiedReq := req.ToUnified()
	unifiedReq.Stream = false

	resp, modelInfo, err := s.router.Chat(ctx, unifiedReq)
	if err != nil {
		return toHTTPError(err)
	}
	if resp == nil {
		return requestError{
			Status:  http.StatusBadGateway,
			Message: "upstream provider returned an empty response",
			Type:    "upstream_error",
		}
	}

	if requestedStream {
		return writeClaudeStream(c, modelInfo.ID, resp)
	}

	claudeResp := translator.FromUnifiedClaude(modelInfo.ID, resp)
	return c.JSON(http.StatusOK, claudeResp)
}

func decodeRequestBody[T any](c echo.Context, target *T) error {
	req := c.Request()
	defer req.Body.Close()

	req.Body = http.MaxBytesReader(c.Response(), req.Body, maxBodyBytes)

	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return requestError{
				Status:  http.StatusBadRequest,
				Message: "request body is required",
				Type:    "invalid_request_error",
			}
		}
		return requestError{
			Status:  http.StatusBadRequest,
			Message: fmt.Sprintf("invalid JSON payload: %v", err),
			Type:    "invalid_request_error",
		}
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return requestError{
			Status:  http.StatusBadRequest,
			Message: "request body must contain a single JSON object",
			Type:    "invalid_request_error",
		}
	}
	return nil
}

type requestError struct {
	Status  int
	Message string
	Type    string
	Code    string
}

func (e requestError) Error() string {
	return e.Message
}

type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

func writeError(c echo.Context, status int, message, errType, code string) error {
	var payload errorBody
	payload.Error.Message = message
	payload.Error.Type = errType
	payload.Error.Code = code
	return c.JSON(status, payload)
}

func openAIErrorHandler(err error, c echo.Context) {
	var reqErr requestError
	if errors.As(err, &reqErr) {
		_ = writeError(c, reqErr.Status, reqErr.Message, reqErr.Type, reqErr.Code)
		return
	}

	type httpError interface {
		Code() int
		Error() string
	}

	if he, ok := err.(httpError); ok {
		_ = writeError(c, he.Code(), he.Error(), "invalid_request_error", "")
		return
	}

	_ = writeError(c, http.StatusInternalServerError, "internal server error", "server_error", "")
}

func toHTTPError(err error) error {
	var reqErr requestError
	if errors.As(err, &reqErr) {
		return reqErr
	}

	if errors.Is(err, provider.ErrUnknownModel) {
		return requestError{
			Status:  http.StatusBadRequest,
			Message: err.Error(),
			Type:    "invalid_request_error",
		}
	}
	if errors.Is(err, provider.ErrUnsupportedOperation) {
		return requestError{
			Status:  http.StatusBadRequest,
			Message: err.Error(),
			Type:    "invalid_request_error",
		}
	}

	return requestError{
		Status:  http.StatusBadGateway,
		Message: "upstream provider error",
		Type:    "upstream_error",
	}
}

func writeSSEEvent(w io.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal SSE payload: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return fmt.Errorf("write SSE event name: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write SSE data: %w", err)
	}
	return nil
}

func printStartupBanner(port int) {
	host := "127.0.0.1"
	fmt.Println()
	fmt.Println("gocode-router ready")
	fmt.Printf("Listening on http://%s:%d\n", host, port)
	fmt.Println("Endpoints:")
	fmt.Println("  GET  /health")
	fmt.Println("  POST /v1/chat/completions")
	fmt.Println("  POST /v1/completions")
	fmt.Println("  POST /v1/messages")
	fmt.Println("Use OpenAI-compatible clients or Claude CLI; configured providers handle translation automatically.")
	fmt.Printf("OpenAI-style example:\n  curl http://%s:%d/v1/chat/completions -H 'Content-Type: application/json' -d '{\"model\":\"claude-3-sonnet\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}'\n", host, port)
	fmt.Printf("Claude CLI example:\n  ANTHROPIC_API_URL=http://%s:%d claude chat -m claude-3-sonnet \"Hello\"\n\n", host, port)
}

func writeClaudeStream(c echo.Context, modelID string, resp *models.UnifiedChatResponse) error {
	writer := c.Response().Writer
	flusher, ok := writer.(http.Flusher)
	if !ok {
		slog.Error("http writer does not support flushing")
		return requestError{
			Status:  http.StatusInternalServerError,
			Message: "server does not support streaming responses",
			Type:    "server_error",
		}
	}

	header := c.Response().Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")

	c.Response().WriteHeader(http.StatusOK)

	usage := map[string]int{
		"input_tokens":  resp.Usage.PromptTokens,
		"output_tokens": resp.Usage.CompletionTokens,
		"total_tokens":  resp.Usage.TotalTokens,
	}

	events := []struct {
		name    string
		payload any
	}{
		{
			name: "message_start",
			payload: map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            resp.ID,
					"type":          "message",
					"role":          resp.Message.Role,
					"model":         modelID,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         usage,
				},
			},
		},
		{
			name: "content_block_start",
			payload: map[string]any{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			},
		},
		{
			name: "content_block_delta",
			payload: map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": resp.Message.Content,
				},
			},
		},
		{
			name: "content_block_stop",
			payload: map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			},
		},
		{
			name: "message_delta",
			payload: map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   resp.FinishReason,
					"stop_sequence": nil,
				},
				"usage": usage,
			},
		},
		{
			name: "message_stop",
			payload: map[string]any{
				"type": "message_stop",
			},
		},
	}

	for _, event := range events {
		if err := writeSSEEvent(writer, event.name, event.payload); err != nil {
			slog.Error("failed to write SSE event", "event", event.name, "err", err)
			return err
		}
		flusher.Flush()
	}

	return nil
}
