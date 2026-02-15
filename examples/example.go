package examples

import "context"

type Speak interface {
	SayHello(ctx context.Context, name string) string
}

type Move interface {
	Walk(ctx context.Context, distance int) string
}
