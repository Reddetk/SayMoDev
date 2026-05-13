package core

// сессии имеют собственный инвариант максимума (5 concurrent), pessimistic lock и отдельный жизненный цикл от токена.
// Покрывает юз-кейсы и эндпоинты :
// POST /iam/auth/logout - jti blacklist + session delete
// Session eviction при 6-м логине (атомарно с созданием)
// DELEE /iam/admin/sessions/:id (admin-initiated termination)
// Публикует события: SessionCreated, SessionTerminated, SessionTerminatedByAdmin
