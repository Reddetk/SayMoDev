package domain

import "github.com/google/uuid"

// UUID — псевдоним google/uuid, чтобы домен не зависел от конкретной библиотеки
// в сигнатурах (замена библиотеки затронет один файл).
type UUID = uuid.UUID
