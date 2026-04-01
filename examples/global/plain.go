package global

import "context"

type Fly interface {
	SayHello(ctx context.Context) string
}
