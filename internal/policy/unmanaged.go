package policy

import "context"

type Unmanaged struct{}

func NewUnmanaged() *Unmanaged {
	return &Unmanaged{}
}

func (u *Unmanaged) Start(ctx context.Context) {
	<-ctx.Done()
}

func (u *Unmanaged) State() string {
	return "ready"
}

func (u *Unmanaged) OnRequest() {}
