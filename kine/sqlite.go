package kine

import (
	"context"

	"github.com/k3s-io/kine/pkg/endpoint"
)

func Kine() (endpoint.ETCDConfig, error) {
	return endpoint.Listen(context.TODO(), endpoint.Config{
		Endpoint: "sqlite://kine.db",
	})
}

