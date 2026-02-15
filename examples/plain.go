package examples

import "context"

//go:generate ddtrace gen

type Fly interface {
	SayHello(ctx context.Context) string
}
