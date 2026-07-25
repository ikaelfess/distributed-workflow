package auth

import (
	"github.com/gin-gonic/gin"
)

type Module struct {
	handler *Handler
}

func NewModule(h *Handler) *Module {
	return &Module{handler: h}
}

func (m *Module) RegisterRoutes(r *gin.Engine) {
	auth := r.Group(GroupRoute)
	auth.POST(RegisterRoute, m.handler.Register)
	auth.POST(LoginRoute, m.handler.Login)
}
