package bootstrap

import (
	"github.com/ikaelfess/distributed-workflow/pkg/httpserver"
	"github.com/ikaelfess/distributed-workflow/pkg/database"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/auth"
)

type App struct {
	Modules []httpserver.RouteModule
}

func New(db *database.Postgres) *App {
	userRepository := auth.NewUserRepository(db.DB)
	authService := auth.NewAuthService(userRepository)
	authHandler := auth.NewAuthHandler(authService)

	return &App{
		Modules: []httpserver.RouteModule{
			auth.NewModule(authHandler),
		},
	}
}
