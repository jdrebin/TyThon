package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/json"
	"github.com/microsoft/TypeScript/tsc/internal/jsonrpc"
	"github.com/microsoft/TypeScript/tsc/internal/locale"
	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	"github.com/microsoft/TypeScript/tsc/internal/vfs"
	"golang.org/x/sync/errgroup"
)

type ServerOptions struct {
	In  Reader
	Out Writer
	Err io.Writer

	Cwd                string
	FS                 vfs.FS
	DefaultLibraryPath string
	TypingsLocation    string
	NpmInstall         func(cwd string, args []string) ([]byte, error)
	Spawn              func(command []string, dir string, stderr io.Writer) (io.ReadWriteCloser, error)
	ProgressDelay      time.Duration
	SetParentProcessID func(parentPID int)
	Python             bool
}

func NewServer(opts *ServerOptions) *Server {
	if opts.Cwd == "" {
		panic("Cwd is required")
	}
	s := &Server{
		r:             opts.In,
		w:             opts.Out,
		stderr:        opts.Err,
		requestQueue:  newDynamicQueue[*lsproto.RequestMessage](),
		outgoingQueue: newDynamicQueue[*lsproto.Message](),
		pendingClientRequests: make(map[jsonrpc.ID]pendingClientRequest),
		pendingServerRequests: make(map[jsonrpc.ID]chan *lsproto.ResponseMessage),
		cwd:          opts.Cwd,
		fs:           opts.FS,
		initComplete: make(chan struct{}),
		pythonMode:   true,
	}
	s.pythonLanguageService = newPythonLanguageService(opts.FS)
	s.logger = newLogger(s)
	if opts.SetParentProcessID != nil {
		s.startWatchdog = opts.SetParentProcessID
	}
	return s
}

type pendingClientRequest struct {
	req    *lsproto.RequestMessage
	cancel context.CancelFunc
}

type Reader interface {
	Read() (*lsproto.Message, error)
}

type Writer interface {
	Write(msg *lsproto.Message) error
}

type lspReader struct {
	r *lsproto.BaseReader
}

type lspWriter struct {
	w *lsproto.BaseWriter
}

type messageMarshalError struct {
	err error
}

func (e *messageMarshalError) Error() string { return "failed to marshal message: " + e.err.Error() }

func (e *messageMarshalError) Unwrap() []error {
	return []error{lsproto.ErrorCodeInternalError, e.err}
}

func (r *lspReader) Read() (*lsproto.Message, error) {
	data, err := r.r.Read()
	if err != nil {
		return nil, err
	}

	req := &lsproto.Message{}
	if err := json.Unmarshal(data, req); err != nil {
		if errors.Is(err, lsproto.ErrorCodeInvalidParams) {
			return req, fmt.Errorf("%w: %w", lsproto.ErrorCodeInvalidParams, err)
		}
		return nil, fmt.Errorf("%w: %w", lsproto.ErrorCodeInvalidRequest, err)
	}

	return req, nil
}

func ToReader(r io.Reader) Reader {
	return &lspReader{r: lsproto.NewBaseReader(r)}
}

func (w *lspWriter) Write(msg *lsproto.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return &messageMarshalError{err: err}
	}
	return w.w.Write(data)
}

func ToWriter(w io.Writer) Writer {
	return &lspWriter{w: lsproto.NewBaseWriter(w)}
}

var (
	_ Reader = (*lspReader)(nil)
	_ Writer = (*lspWriter)(nil)
)

type Server struct {
	r             Reader
	w             Writer
	backgroundCtx context.Context
	stderr        io.Writer

	logger                  *logger
	initStarted             atomic.Bool
	clientSeq               atomic.Int32
	requestQueue            *dynamicQueue[*lsproto.RequestMessage]
	outgoingQueue           *dynamicQueue[*lsproto.Message]
	pendingClientRequests   map[jsonrpc.ID]pendingClientRequest
	pendingClientRequestsMu sync.Mutex
	pendingServerRequests   map[jsonrpc.ID]chan *lsproto.ResponseMessage
	pendingServerRequestsMu sync.Mutex

	cwd string
	fs  vfs.FS

	initializeParams      *lsproto.InitializeParams
	initializationOptions *lsproto.InitializationOptions
	clientCapabilities    lsproto.ResolvedClientCapabilities
	positionEncoding      lsproto.PositionEncodingKind
	pythonMode            bool
	pythonLanguageService *pythonLanguageService
	localeMu              sync.RWMutex
	locale                locale.Locale
	initLocale            locale.Locale

	telemetryEnabled  bool
	lastRequestTimeMs atomic.Int64
	initComplete      chan struct{}
	startWatchdog     func(parentPID int)
}

func (s *Server) InitComplete() <-chan struct{} { return s.initComplete }

func (s *Server) GetLocale() locale.Locale {
	s.localeMu.RLock()
	defer s.localeMu.RUnlock()
	return s.locale
}

func (s *Server) SetLocale(localeString string) {
	newLocale := s.initLocale
	if localeString != "auto" {
		parsed, ok := locale.Parse(localeString)
		if !ok {
			return
		}
		newLocale = parsed
	}
	s.localeMu.Lock()
	s.locale = newLocale
	s.localeMu.Unlock()
}

func (s *Server) SetCompilerOptionsForInferredProjects(context.Context, *core.CompilerOptions) {}

func (s *Server) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	s.backgroundCtx = ctx
	g.Go(func() error { return s.dispatchLoop(ctx) })
	g.Go(func() error { return s.writeLoop(ctx) })

	// Don't run readLoop in the group, as it blocks on stdin read and cannot be cancelled.
	readLoopErr := make(chan error, 1)
	g.Go(func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readLoopErr:
			return err
		}
	})
	go func() { readLoopErr <- s.readLoop(ctx) }()

	if err := g.Wait(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() != nil {
		return err
	}
	return nil
}

func (s *Server) readLoop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := s.read()
		if err != nil {
			if errors.Is(err, lsproto.ErrorCodeInvalidRequest) || errors.Is(err, lsproto.ErrorCodeInvalidParams) {
				var id *jsonrpc.ID
				if errors.Is(err, lsproto.ErrorCodeInvalidParams) {
					if msg != nil && msg.Kind == jsonrpc.MessageKindRequest {
						id = msg.AsRequest().ID
					}
				}
				if err := s.sendError(id, err); err != nil {
					return err
				}
				continue
			}
			return err
		}

		if s.initializeParams == nil && msg.Kind == jsonrpc.MessageKindRequest {
			req := msg.AsRequest()
			if req.Method == lsproto.MethodInitialize {
				params, err := lsproto.UnmarshalParams[*lsproto.InitializeParams](req)
				if err != nil {
					if err := s.sendError(req.ID, err); err != nil {
						return err
					}
					continue
				}
				resp, err := s.handleInitialize(ctx, params, req)
				if err != nil {
					return err
				}
				if err := s.sendResult(req.ID, resp); err != nil {
					return err
				}
			} else {
				if err := s.sendError(req.ID, lsproto.ErrorCodeServerNotInitialized); err != nil {
					return err
				}
			}
			continue
		}

		if msg.Kind == jsonrpc.MessageKindResponse {
			resp := msg.AsResponse()
			s.pendingServerRequestsMu.Lock()
			if respChan, ok := s.pendingServerRequests[*resp.ID]; ok {
				respChan <- resp
				close(respChan)
				delete(s.pendingServerRequests, *resp.ID)
			}
			s.pendingServerRequestsMu.Unlock()
		} else {
			req := msg.AsRequest()
			if req.Method == lsproto.MethodCancelRequest {
				if params, err := lsproto.UnmarshalParams[*lsproto.CancelParams](req); err == nil && params != nil {
					s.cancelRequest(params.Id)
				}
			} else {
				if err := s.requestQueue.Put(ctx, req); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Server) cancelRequest(rawID lsproto.IntegerOrString) {
	id := lsproto.NewID(rawID)
	s.pendingClientRequestsMu.Lock()
	defer s.pendingClientRequestsMu.Unlock()
	if pendingReq, ok := s.pendingClientRequests[*id]; ok {
		pendingReq.cancel()
		delete(s.pendingClientRequests, *id)
	}
}

func (s *Server) read() (*lsproto.Message, error) {
	return s.r.Read()
}

func (s *Server) dispatchLoop(ctx context.Context) error {
	ctx, lspExit := context.WithCancelCause(ctx)
	defer lspExit(nil)
	for {
		req, err := s.requestQueue.Get(ctx)
		if err != nil {
			// Preserve an orderly LSP exit (io.EOF) instead of replacing its
			// cause with context.Canceled when the dispatch queue wakes up.
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}

		s.lastRequestTimeMs.Store(time.Now().UnixMilli())
		requestCtx := locale.WithLocale(ctx, s.GetLocale())
		var cancel context.CancelFunc
		if req.ID != nil {
			requestCtx, cancel = context.WithCancel(core.WithRequestID(requestCtx, req.ID.String()))
			s.pendingClientRequestsMu.Lock()
			s.pendingClientRequests[*req.ID] = pendingClientRequest{
				req:    req,
				cancel: cancel,
			}
			s.pendingClientRequestsMu.Unlock()
		}

		handleError := func(err error) {
			if errors.Is(err, context.Canceled) {
				if err := s.sendError(req.ID, lsproto.ErrorCodeRequestCancelled); err != nil {
					lspExit(err)
				}
			} else if errors.Is(err, io.EOF) {
				lspExit(io.EOF)
			} else {
				if err := s.sendError(req.ID, err); err != nil {
					lspExit(err)
				}
			}
		}

		removeRequest := func() {
			if req.ID != nil {
				defer cancel()
				s.pendingClientRequestsMu.Lock()
				defer s.pendingClientRequestsMu.Unlock()
				delete(s.pendingClientRequests, *req.ID)
			}
		}

		if doAsyncWork, err := s.handleRequestOrNotification(requestCtx, req); err != nil {
			handleError(err)
			removeRequest()
		} else if doAsyncWork != nil {
			go func() {
				if lsError := doAsyncWork(); lsError != nil {
					handleError(lsError)
				}
				removeRequest()
			}()
		} else {
			removeRequest()
		}
	}
}

func (s *Server) writeLoop(ctx context.Context) error {
	for {
		msg, err := s.outgoingQueue.Get(ctx)
		if err != nil {
			return err
		}
		if err := s.w.Write(msg); err != nil {
			var marshalErr *messageMarshalError
			if errors.As(err, &marshalErr) && msg.Kind == jsonrpc.MessageKindResponse {
				if resp := msg.AsResponse(); resp.ID != nil && resp.Error == nil {
					s.logger.Errorf("failed to marshal response for request %s: %v", resp.ID, marshalErr)
					if sendErr := s.sendError(resp.ID, marshalErr); sendErr != nil {
						return sendErr
					}
					continue
				}
			}
			return fmt.Errorf("failed to write message: %w", err)
		}
	}
}

// WARNING: this should only be called in the async portion of a request handler,
// otherwise a deadlock can occur.
func sendClientRequest[Req, Resp any](ctx context.Context, s *Server, info lsproto.RequestInfo[Req, Resp], params Req) (Resp, error) {
	id := jsonrpc.NewIDString(fmt.Sprintf("ts%d", s.clientSeq.Add(1)))
	req := info.NewRequestMessage(id, params)

	responseChan := make(chan *lsproto.ResponseMessage, 1)
	s.pendingServerRequestsMu.Lock()
	s.pendingServerRequests[*id] = responseChan
	s.pendingServerRequestsMu.Unlock()

	defer func() {
		s.pendingServerRequestsMu.Lock()
		defer s.pendingServerRequestsMu.Unlock()
		if respChan, ok := s.pendingServerRequests[*id]; ok {
			close(respChan)
			delete(s.pendingServerRequests, *id)
		}
	}()

	if err := s.send(req.Message()); err != nil {
		return *new(Resp), err
	}

	select {
	case <-ctx.Done():
		return *new(Resp), ctx.Err()
	case resp := <-responseChan:
		if resp.Error != nil {
			return *new(Resp), fmt.Errorf("request failed: %s", resp.Error.String())
		}
		return info.UnmarshalResult(resp.Result)
	}
}

// sendClientRequestFireAndForget sends a request to the client without waiting for a response.
// The response, if any, will be silently ignored by the read loop since no pending channel is registered.
// This means any error returned by the client will not be observed. Use only for requests where the
// response value is not needed (e.g., the client always returns null).
func sendClientRequestFireAndForget[Req, Resp any](s *Server, info lsproto.RequestInfo[Req, Resp], params Req) error {
	id := jsonrpc.NewIDString(fmt.Sprintf("ts%d", s.clientSeq.Add(1)))
	req := info.NewRequestMessage(id, params)
	return s.send(req.Message())
}

func (s *Server) sendResult(id *jsonrpc.ID, result any) error {
	return s.sendResponse(&lsproto.ResponseMessage{
		ID:     id,
		Result: result,
	})
}

type userFacingRequestFailedError string

func (e userFacingRequestFailedError) Error() string { return string(e) }
func (e userFacingRequestFailedError) Unwrap() error { return lsproto.ErrorCodeRequestFailed }

func (s *Server) sendError(id *jsonrpc.ID, err error) error {
	// Do not send error response for notifications,
	// except for parse errors which may occur before determining if the message is a request or notification.
	if id == nil && !errors.Is(err, lsproto.ErrorCodeInvalidRequest) {
		s.logger.Errorf("error handling notification: %s", err)
		return nil
	}
	code := lsproto.ErrorCodeInternalError
	if errCode, ok := errors.AsType[lsproto.ErrorCode](err); ok {
		code = errCode
	}
	// TODO(jakebailey): error data
	return s.sendResponse(&lsproto.ResponseMessage{
		ID: id,
		Error: &jsonrpc.ResponseError{
			Code:    int32(code),
			Message: err.Error(),
		},
	})
}

func sendNotification[Params any](s *Server, info lsproto.NotificationInfo[Params], params Params) error {
	return s.send(info.NewNotificationMessage(params).Message())
}

func (s *Server) sendResponse(resp *lsproto.ResponseMessage) error {
	return s.send(resp.Message())
}

// send writes a message to the outgoing queue, respecting context cancellation.
func (s *Server) send(msg *lsproto.Message) error {
	return s.outgoingQueue.Put(s.backgroundCtx, msg)
}

func (s *Server) handleRequestOrNotification(ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
	ctx = lsproto.WithClientCapabilities(ctx, &s.clientCapabilities)
	if handler := handlers()[req.Method]; handler != nil {
		start := time.Now()
		doAsyncWork, err := handler(s, ctx, req)
		idStr := ""
		if req.ID != nil {
			idStr = " (" + req.ID.String() + ")"
		}
		if err != nil {
			if _, ok := errors.AsType[userFacingRequestFailedError](err); !ok {
				s.logger.Error("error handling method '", req.Method, "'", idStr, ": ", err)
			} else if !s.logger.IsTracing() {
				s.logger.Info("handled method '", req.Method, "'", idStr, " in ", time.Since(start))
			}
			return nil, err
		}
		if doAsyncWork != nil {
			return func() error {
				asyncWorkErr := doAsyncWork()
				_, isUserFacing := errors.AsType[userFacingRequestFailedError](asyncWorkErr)
				isRealError := asyncWorkErr != nil && !isUserFacing
				if isRealError {
					s.logger.Info("error handling method '", req.Method, "'", idStr, " in ", time.Since(start))
				} else if !s.logger.IsTracing() {
					s.logger.Info("handled method '", req.Method, "'", idStr, " in ", time.Since(start))
				}
				return asyncWorkErr
			}, nil
		}
		if !s.logger.IsTracing() {
			s.logger.Info("handled method '", req.Method, "'", idStr, " in ", time.Since(start))
		}
		return nil, nil
	}
	s.logger.Warn("unknown method '", req.Method, "'")
	if req.ID != nil {
		return nil, s.sendError(req.ID, lsproto.ErrorCodeInvalidRequest)
	}
	return nil, nil
}

type handlerMap map[lsproto.Method]func(*Server, context.Context, *lsproto.RequestMessage) (func() error, error)

var handlers = sync.OnceValue(func() handlerMap {
	handlers := make(handlerMap)
	registerRequestHandler(handlers, lsproto.InitializeInfo, (*Server).handleInitialize)
	registerNotificationHandler(handlers, lsproto.InitializedInfo, (*Server).handleInitialized)
	registerRequestHandler(handlers, lsproto.ShutdownInfo, (*Server).handleShutdown)
	registerNotificationHandler(handlers, lsproto.ExitInfo, (*Server).handleExit)
	registerNotificationHandler(handlers, lsproto.TextDocumentDidOpenInfo, (*Server).handleDidOpen)
	registerNotificationHandler(handlers, lsproto.TextDocumentDidChangeInfo, (*Server).handleDidChange)
	registerNotificationHandler(handlers, lsproto.TextDocumentDidSaveInfo, (*Server).handleDidSave)
	registerNotificationHandler(handlers, lsproto.TextDocumentDidCloseInfo, (*Server).handleDidClose)
	registerNotificationHandler(handlers, lsproto.SetTraceInfo, (*Server).handleSetTrace)
	registerNotificationHandler(handlers, lsproto.CustomSetLogVerbosityInfo, (*Server).handleSetLogVerbosity)
	registerCompletionRequestHandler(handlers)
	registerDiagnosticRequestHandler(handlers)
	registerHoverRequestHandler(handlers)
	registerSignatureHelpRequestHandler(handlers)
	registerPythonDefinitionHandlers(handlers)
	registerPythonSemanticTokenHandlers(handlers)
	return handlers
})

func registerNotificationHandler[Req any](handlers handlerMap, info lsproto.NotificationInfo[Req], fn func(*Server, context.Context, Req) error) {
	handlers[info.Method] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[Req](req)
		if err != nil {
			return nil, err
		}
		if err := fn(s, ctx, params); err != nil {
			return nil, err
		}
		return nil, ctx.Err()
	}
}

func registerRequestHandler[Req, Resp any](
	handlers handlerMap,
	info lsproto.RequestInfo[Req, Resp],
	fn func(*Server, context.Context, Req, *lsproto.RequestMessage) (Resp, error),
) {
	handlers[info.Method] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[Req](req)
		if err != nil {
			return nil, err
		}
		resp, err := fn(s, ctx, params, req)
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, s.sendResult(req.ID, resp)
	}
}
func (s *Server) recover(req *lsproto.RequestMessage) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		s.logger.Errorf("panic handling request %s: %v\n%s", req.Method, r, string(stack))
		if req.ID != nil {
			_ = s.sendError(req.ID, fmt.Errorf("%w: panic handling request %s: %v", lsproto.ErrorCodeInternalError, req.Method, r))
		} else {
			s.logger.Error("unhandled panic in notification", req.Method, r)
		}

		if s.telemetryEnabled {
			_ = sendNotification(s, lsproto.TelemetryEventInfo, lsproto.TelemetryEvent{
				RequestFailureTelemetryEvent: &lsproto.RequestFailureTelemetryEvent{
					Properties: &lsproto.RequestFailureTelemetryProperties{
						ErrorCode:     lsproto.ErrorCodeInternalError.String(),
						RequestMethod: strings.ReplaceAll(string(req.Method), "/", "."),
						Stack:         sanitizeStackTrace(string(stack)),
					},
				},
			})
		}
	}
}

func registerCompletionRequestHandler(handlers handlerMap) {
	handlers[lsproto.MethodTextDocumentCompletion] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[*lsproto.CompletionParams](req)
		if err != nil {
			return nil, err
		}
		return func() error {
			defer s.recover(req)
			resp, completionErr := s.pythonLanguageService.provideCompletion(ctx, params, s.positionEncoding)
			if completionErr != nil {
				return completionErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.sendResult(req.ID, resp)
		}, nil
	}
}

func registerDiagnosticRequestHandler(handlers handlerMap) {
	handlers[lsproto.MethodTextDocumentDiagnostic] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[*lsproto.DocumentDiagnosticParams](req)
		if err != nil {
			return nil, err
		}
		return func() error {
			defer s.recover(req)
			resp, diagnosticErr := s.pythonLanguageService.provideDiagnostics(ctx, params, s.positionEncoding)
			if diagnosticErr != nil {
				return diagnosticErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.sendResult(req.ID, resp)
		}, nil
	}
}

func registerHoverRequestHandler(handlers handlerMap) {
	handlers[lsproto.MethodTextDocumentHover] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[*lsproto.HoverParams](req)
		if err != nil {
			return nil, err
		}
		return func() error {
			defer s.recover(req)
			resp, hoverErr := s.pythonLanguageService.provideHover(ctx, params, s.positionEncoding)
			if hoverErr != nil {
				return hoverErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.sendResult(req.ID, resp)
		}, nil
	}
}

func registerSignatureHelpRequestHandler(handlers handlerMap) {
	handlers[lsproto.MethodTextDocumentSignatureHelp] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[*lsproto.SignatureHelpParams](req)
		if err != nil {
			return nil, err
		}
		return func() error {
			defer s.recover(req)
			resp, signatureErr := s.pythonLanguageService.provideSignatureHelp(ctx, params, s.positionEncoding)
			if signatureErr != nil {
				return signatureErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.sendResult(req.ID, resp)
		}, nil
	}
}

func (s *Server) handleInitialize(ctx context.Context, params *lsproto.InitializeParams, _ *lsproto.RequestMessage) (lsproto.InitializeResponse, error) {
	if s.initializeParams != nil {
		return nil, lsproto.ErrorCodeInvalidRequest
	}
	s.initStarted.Store(true)
	s.initializeParams = params
	if params.InitializationOptions != nil && params.InitializationOptions.InitializationOptions != nil {
		s.initializationOptions = params.InitializationOptions.InitializationOptions
	} else {
		s.initializationOptions = &lsproto.InitializationOptions{}
	}
	if s.initializationOptions.LogVerbosity != nil {
		if v := *s.initializationOptions.LogVerbosity; isValidLogVerbosity(v) {
			s.logger.SetVerbosity(v)
		}
	}
	s.clientCapabilities = params.Capabilities.Resolve()
	s.positionEncoding = lsproto.PositionEncodingKindUTF16
	if slices.Contains(s.clientCapabilities.General.PositionEncodings, lsproto.PositionEncodingKindUTF8) {
		s.positionEncoding = lsproto.PositionEncodingKindUTF8
	}
	if s.initializeParams.Locale != nil {
		s.locale, _ = locale.Parse(*s.initializeParams.Locale)
	}
	s.initLocale = s.locale
	if s.startWatchdog != nil && params.ProcessId.Integer != nil {
		s.startWatchdog(int(*params.ProcessId.Integer))
	}
	pythonCompletionTriggers := []string{".", "[", `"`, "'", "_"}
	pythonSignatureTriggers := []string{"(", ","}
	pythonSignatureRetriggers := []string{","}
	return &lsproto.InitializeResult{
		ServerInfo: &lsproto.ServerInfo{Name: "TyThon", Version: new(core.Version())},
		Capabilities: &lsproto.ServerCapabilities{
			SemanticTokensProvider: &lsproto.SemanticTokensOptionsOrRegistrationOptions{
				Options: &lsproto.SemanticTokensOptions{
					Legend: SemanticTokensLegend(s.clientCapabilities.TextDocument.SemanticTokens),
					Full:   &lsproto.BooleanOrSemanticTokensFullDelta{Boolean: new(true)},
					Range:  &lsproto.BooleanOrEmptyObject{Boolean: new(true)},
				},
			},
			PositionEncoding: new(s.positionEncoding),
			TextDocumentSync: &lsproto.TextDocumentSyncOptionsOrKind{
				Options: &lsproto.TextDocumentSyncOptions{
					OpenClose: new(true),
					Change:    new(lsproto.TextDocumentSyncKindFull),
				},
			},
			HoverProvider:      &lsproto.BooleanOrHoverOptions{Boolean: new(true)},
			DefinitionProvider: &lsproto.BooleanOrDefinitionOptions{Boolean: new(true)},
			DiagnosticProvider: &lsproto.DiagnosticOptionsOrRegistrationOptions{
				Options: &lsproto.DiagnosticOptions{Identifier: new("typed-python"), InterFileDependencies: true},
			},
			CompletionProvider: &lsproto.CompletionOptions{
				TriggerCharacters: &pythonCompletionTriggers,
				ResolveProvider:   new(false),
			},
			SignatureHelpProvider: &lsproto.SignatureHelpOptions{
				TriggerCharacters:   &pythonSignatureTriggers,
				RetriggerCharacters: &pythonSignatureRetriggers,
			},
		},
	}, nil
}

func (s *Server) handleInitialized(context.Context, *lsproto.InitializedParams) error {
	close(s.initComplete)
	return nil
}

func (s *Server) handleShutdown(context.Context, lsproto.NoParams, *lsproto.RequestMessage) (lsproto.ShutdownResponse, error) {
	return lsproto.ShutdownResponse{}, nil
}

func (s *Server) handleExit(context.Context, lsproto.NoParams) error { return io.EOF }

func (s *Server) handleDidOpen(_ context.Context, params *lsproto.DidOpenTextDocumentParams) error {
	s.pythonLanguageService.open(params.TextDocument.Uri, params.TextDocument.Version, params.TextDocument.Text)
	return nil
}

func (s *Server) handleDidChange(_ context.Context, params *lsproto.DidChangeTextDocumentParams) error {
	return s.pythonLanguageService.change(params.TextDocument.Uri, params.TextDocument.Version, params.ContentChanges)
}

func (s *Server) handleDidSave(context.Context, *lsproto.DidSaveTextDocumentParams) error { return nil }

func (s *Server) handleDidClose(_ context.Context, params *lsproto.DidCloseTextDocumentParams) error {
	s.pythonLanguageService.close(params.TextDocument.Uri)
	return nil
}

func (s *Server) handleSetTrace(context.Context, *lsproto.SetTraceParams) error { return nil }

func (s *Server) handleSetLogVerbosity(_ context.Context, params *lsproto.SetLogVerbosityParams) error {
	if !isValidLogVerbosity(params.Verbosity) {
		return fmt.Errorf("%w: invalid log verbosity %d", lsproto.ErrorCodeInvalidParams, params.Verbosity)
	}
	s.logger.SetVerbosity(params.Verbosity)
	return nil
}
