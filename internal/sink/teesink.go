package sink

import (
	"context"
	"errors"

	"github.com/alterfo/kb/internal/connector"
)

type TeeSink struct {
	sinks []connector.Sink
}

func NewTeeSink(sinks ...connector.Sink) *TeeSink {
	return &TeeSink{sinks: sinks}
}

func (t *TeeSink) Write(ctx context.Context, d connector.Document) error {
	var errs []error
	for _, s := range t.sinks {
		if err := s.Write(ctx, d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *TeeSink) Prune(ctx context.Context, sourceName string, seen map[string]struct{}, prefixes ...string) error {
	var errs []error
	for _, s := range t.sinks {
		if err := s.Prune(ctx, sourceName, seen, prefixes...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *TeeSink) Tombstone(ctx context.Context, sourceName, id string) error {
	var errs []error
	for _, s := range t.sinks {
		if err := s.Tombstone(ctx, sourceName, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
