package examples

import "context"

type Fly interface {
	SayHello(ctx context.Context) string
}
