package middleware

// Константы ролей для gate-логики первичного адаптера.
//
// Значения соответствуют полю Role в AuthContext (JWT claim "role").
// Источник истины  core/consts, но первичный адаптер не зависит
// от сервисного уровня. Эти константы  локальная копия для нужд
// middleware и handlers.
const (
	RoleAdministrator = "administrator"
	RolePatient       = "patient"
	RoleRelative      = "relative"
)
