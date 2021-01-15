package handler

import (
	"devture-matrix-corporal/corporal/hook"
	"devture-matrix-corporal/corporal/httpgateway/hookrunner"
	"devture-matrix-corporal/corporal/httpgateway/interceptor"
	"devture-matrix-corporal/corporal/httphelp"
	"net/http"
	"net/http/httputil"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type loginHandler struct {
	reverseProxy     *httputil.ReverseProxy
	hookRunner       *hookrunner.HookRunner
	loginInterceptor interceptor.Interceptor
	logger           *logrus.Logger
}

func NewLoginHandler(
	reverseProxy *httputil.ReverseProxy,
	hookRunner *hookrunner.HookRunner,
	loginInterceptor interceptor.Interceptor,
	logger *logrus.Logger,
) *loginHandler {
	return &loginHandler{
		reverseProxy:     reverseProxy,
		hookRunner:       hookRunner,
		loginInterceptor: loginInterceptor,
		logger:           logger,
	}
}

func (me *loginHandler) RegisterRoutesWithRouter(router *mux.Router) {
	router.Handle(
		"/_matrix/client/r0/login",
		me.createInterceptorHandler("login", me.loginInterceptor),
	).Methods("POST")
}

func (me *loginHandler) createInterceptorHandler(name string, interceptorObj interceptor.Interceptor) http.HandlerFunc {
	hooksToRun := []string{
		hook.EventTypeBeforeAnyRequest,
		hook.EventTypeBeforeUnauthenticatedRequest,
		hook.EventTypeAfterAnyRequest,
		hook.EventTypeAfterUnauthenticatedRequest,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		logger := me.logger.WithField("method", r.Method)
		logger = logger.WithField("uri", r.RequestURI)
		logger = logger.WithField("handler", name)

		httpResponseModifierFuncs := make([]hook.HttpResponseModifierFunc, 0)

		// This "runs" both before and after hooks.
		// Before hooks run early on and may abort execution right here.
		// After hooks just schedule HTTP response modifier functions and will actually run later on.
		for _, eventType := range hooksToRun {
			if !runHook(me.hookRunner, eventType, w, r, logger, &httpResponseModifierFuncs) {
				return
			}
		}

		interceptorResult := interceptorObj.Intercept(r)

		logger = logger.WithFields(interceptorResult.LoggingContextFields)

		if interceptorResult.Result == interceptor.InterceptorResultDeny {
			logger.Infof(
				"HTTP gateway (intercepted): denying (%s: %s)",
				interceptorResult.ErrorCode,
				interceptorResult.ErrorMessage,
			)

			httphelp.RespondWithMatrixError(
				w,
				http.StatusForbidden,
				interceptorResult.ErrorCode,
				interceptorResult.ErrorMessage,
			)

			return
		}

		if interceptorResult.Result == interceptor.InterceptorResultProxy {
			reverseProxyToUse := me.reverseProxy

			if len(httpResponseModifierFuncs) == 0 {
				logger.Debugf("HTTP gateway (intercepted): proxying")
			} else {
				logger.Debugf("HTTP gateway (intercepted): proxying (with response modification)")

				reverseProxyCopy := *reverseProxyToUse
				reverseProxyCopy.ModifyResponse = hook.CreateChainedHttpResponseModifierFunc(httpResponseModifierFuncs)
				reverseProxyToUse = &reverseProxyCopy
			}

			reverseProxyToUse.ServeHTTP(w, r)

			return
		}

		logger.Fatalf("HTTP gateway (intercepted): unexpected interceptor result: %#v", interceptorResult)
	}
}

// Ensure interface is implemented
var _ httphelp.HandlerRegistrator = &loginHandler{}
