package auth

const (
	GroupRoute = "/auth"

	RegisterRoute = "/register"
	FullRegisterRoute = GroupRoute + RegisterRoute

	LoginRoute = "/login"
	FullLoginRoute = GroupRoute + LoginRoute
)
