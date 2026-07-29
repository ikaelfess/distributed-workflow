package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/gin-contrib/logger"
)

type RouteModule interface {
	RegisterRoutes(*gin.Engine)
}

type server struct {
	srv    *http.Server
	engine *gin.Engine
	logger zerolog.Logger
}

type Config struct {
	WriteTimeout      time.Duration
	ReadTimeout       time.Duration
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	Logger            zerolog.Logger
	Addr              string
	Middlewares       []gin.HandlerFunc
}

func New(cfg Config) *server {
	engine := gin.New()
	engine.Use(logger.SetLogger(
		logger.WithLogger(
			func(_ *gin.Context, _ zerolog.Logger) zerolog.Logger {
				return cfg.Logger
			},
		),
	))
	engine.Use(gin.Recovery())

	if len(cfg.Middlewares) > 0 {
		engine.Use(cfg.Middlewares...)
	}

	srv := &http.Server{
		WriteTimeout:      cfg.WriteTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		Handler:           engine,
		Addr:              cfg.Addr,
	}

	return &server{
		engine: engine,
		srv:    srv,
		logger: cfg.Logger,
	}
}

func (s *server) RegisterModules(mods ...RouteModule) {
	for _, m := range mods {
		m.RegisterRoutes(s.engine)
	}
}

func (s *server) Start() {
	s.logger.Info().Str("addr", s.srv.Addr).Msg("started")
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Fatal().Err(err).Msg("server failed")
	}
}

func (s *server) Close(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
