package metrics

// goca:manual — безопасная регистрация: повторная регистрация не паникует.

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

func register(registry *prometheus.Registry, collectors ...prometheus.Collector) {
	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				continue
			}
			panic(err)
		}
	}
}
