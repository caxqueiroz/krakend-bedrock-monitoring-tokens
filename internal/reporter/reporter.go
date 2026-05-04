package reporter

import (
	"context"
	"errors"
	"io"
	"os"

	"krakendBedRockPlugin/internal/usage"
)

type Reporter interface {
	Record(context.Context, usage.Usage) error
	Close() error
}

type Multi struct {
	Reporters []Reporter
}

func (m Multi) Record(ctx context.Context, u usage.Usage) error {
	var errs []error
	for _, r := range m.Reporters {
		if r == nil {
			continue
		}
		if err := safeRecord(ctx, r, u); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m Multi) Close() error {
	var errs []error
	for _, r := range m.Reporters {
		if r == nil {
			continue
		}
		if err := r.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func safeRecord(ctx context.Context, r Reporter, u usage.Usage) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.Join(err, ErrReporterPanic)
		}
	}()
	return r.Record(ctx, u)
}

var ErrReporterPanic = errors.New("reporter panic")

func defaultWriter(w io.Writer) io.Writer {
	if w == nil {
		return os.Stdout
	}
	return w
}
