package view

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotImplemented is what a StatusDeclared resource returns from every
// operation. It mirrors the refusal pkg/collection makes for a declared
// Collection method and the one internal/forge/pluginscaffold generates
// for a declared sync plugin, for the same reason all three exist: a
// skeleton that silently succeeds is indistinguishable from a working
// implementation with nothing to do, and the second one is the failure
// this project has already shipped twice.
var ErrNotImplemented = errors.New("view resource is declared but not implemented")

// FieldFault is a write failure a port can blame on one declared field.
//
// It exists because some failures are only knowable at the port. A runbook
// id that names nothing, a device type this build does not register, a name
// already taken -- none of these can be checked by the shared validator,
// and all of them are the submitter's mistake rather than the platform's.
// Without this they surface as a 500 and an error page, which tells
// somebody who mistyped a runbook name that the server is broken.
//
// A port returns one (or wraps one), and Bind turns it into the same
// FieldErrors a validation failure produces, so the form redisplays with
// the message attached to the control that caused it.
type FieldFault struct {
	// Field is the declared Field.Name to attach the message to.
	Field string

	// Message is what the user reads. It is written for them rather than
	// for a log: a port's own error text can carry a filesystem path or
	// a storage detail and must not be passed through.
	Message string
}

// Error implements error.
func (e FieldFault) Error() string { return e.Field + ": " + e.Message }

// faultErrors converts a port error into per-field errors when it blames a
// field, and returns nil otherwise so the caller treats it as a real
// failure.
func faultErrors(err error) FieldErrors {
	var fault FieldFault
	if !errors.As(err, &fault) {
		return nil
	}
	errs := FieldErrors{}
	errs.Add(fault.Field, fault.Message)
	return errs
}

// Page is one page of a resource's own domain type, before erasure.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// Reader is the read half of a resource's port. Every resource supplies
// one; a resource that cannot be read has nothing to render.
type Reader[T any] interface {
	List(ctx context.Context, q Query) (Page[T], error)
	Get(ctx context.Context, id string) (T, error)
}

// Writer is the write half, supplied only by resources that genuinely
// support writes.
//
// It is a separate interface from Reader on purpose. Runbooks are read
// from a directory and jobs are created only by the dispatcher, so a
// single fat Resource[T] would force both to stub three methods that must
// never be called -- and a stub that must never be called is a stub
// somebody eventually calls. A descriptor with no Writer simply has no
// create, update, or delete handler, and the templates render no button
// for an operation that does not exist.
type Writer[T any] interface {
	Create(ctx context.Context, v T) (id string, err error)
	Update(ctx context.Context, id string, v T) error
	Delete(ctx context.Context, id string) error
}

// Creator is the create-only half of a write port.
//
// It exists for the resource that can be brought into being but never
// edited or removed, which is not a rare shape: a job is dispatched and
// then only observed, because editing a running fan-out is meaningless and
// deleting one would destroy the audit record it exists to be. Making such
// a resource satisfy Writer would mean two methods that must never be
// called, and the whole reason Reader and Writer are separate is that a
// stub which must never be called is a stub somebody eventually calls.
type Creator[T any] interface {
	Create(ctx context.Context, v T) (id string, err error)
}

// Projector is the pair of translations only a resource author can write:
// domain type to presentation, and submission back to domain type.
//
// These two functions are the entire per-resource cost of this design.
// Everything else -- routing, templates, validation, affordances, paging,
// the mobile layout -- is written once and shared.
type Projector[T any] struct {
	// Row erases one record into its presentation values.
	Row func(T) Row

	// Form produces the values that prefill an edit form. Required only
	// when a Writer is present.
	Form func(T) map[string]string

	// Bind parses a submission back into the domain type, reporting
	// per-field problems the shared Validate could not know about
	// (uniqueness, cross-field rules, anything requiring domain
	// knowledge). Required only when a Writer is present.
	Bind func(Values) (T, FieldErrors)
}

// Handlers is the type-erased operation set a Descriptor carries. Every
// field is nil for an operation the resource does not support, so a
// handler's nil check and a template's button both read from the same
// fact.
//
// This is the only place in the UI where a generic type becomes a
// non-generic one, and that is deliberate: pkg/registry.Registry[T]
// instantiates at a concrete T, so a single registry cannot hold
// Resource[Device] and Resource[Job] without falling back to any and a
// per-request type assertion -- exactly what pkg/registry's own doc
// comment criticises. Erasing once, at the presentation boundary, keeps
// the author's side fully typed and the registry's side fully concrete.
type Handlers struct {
	List   func(ctx context.Context, q Query) (RowPage, error)
	Get    func(ctx context.Context, id string) (Row, error)
	Form   func(ctx context.Context, id string) (map[string]string, error)
	Create func(ctx context.Context, v Values) (id string, errs FieldErrors, err error)
	Update func(ctx context.Context, id string, v Values) (FieldErrors, error)
	Delete func(ctx context.Context, id string) error
}

// Writable reports whether these handlers support any state change. A
// descriptor whose handlers are read-only never renders a create button,
// an edit link, or a delete control.
func (h *Handlers) Writable() bool {
	return h != nil && h.Create != nil && h.Update != nil && h.Delete != nil
}

// MustBind is Bind, panicking on an invalid projector. Resource packages
// call it from their own init(), so a projector missing a function fails
// at process start rather than as a nil-pointer dereference on whichever
// page first exercises it -- the same reason MustRegister exists beside
// Register.
func MustBind[T any](r Reader[T], w Writer[T], p Projector[T]) *Handlers {
	h, err := Bind(r, w, p)
	if err != nil {
		panic("view: " + err.Error())
	}
	return h
}

// MustBindCreatable is BindCreatable, panicking on an invalid projector.
func MustBindCreatable[T any](r Reader[T], c Creator[T], p Projector[T]) *Handlers {
	h, err := BindCreatable(r, c, p)
	if err != nil {
		panic("view: " + err.Error())
	}
	return h
}

// BindCreatable erases a resource that can be read and created but never
// updated or deleted.
//
// The resulting Handlers carry no Update and no Delete, so Writable()
// reports false and no template renders an edit link or a delete control --
// the same fact drives the handler's nil check and the button, exactly as
// it does for a read-only resource.
func BindCreatable[T any](r Reader[T], c Creator[T], p Projector[T]) (*Handlers, error) {
	h, err := Bind[T](r, nil, p)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("resource has no Creator")
	}
	if p.Bind == nil {
		return nil, fmt.Errorf("projector has a Creator but no Bind function")
	}

	h.Create = func(ctx context.Context, v Values) (string, FieldErrors, error) {
		item, errs := p.Bind(v)
		if errs.Any() {
			// Return before touching the port, so a validation failure
			// cannot leave a half-written record behind.
			return "", errs, nil
		}
		id, err := c.Create(ctx, item)
		if errs := faultErrors(err); errs != nil {
			return "", errs, nil
		}
		return id, nil, err
	}
	return h, nil
}

// Bind erases a typed resource into the Handlers a Descriptor carries.
//
// Passing a nil Writer produces read-only handlers. Note that this means a
// genuinely nil interface value: a nil *concrete* type stored in a Writer
// interface is not nil here, and would produce handlers that dereference
// it. Resources pass an untyped nil literal.
func Bind[T any](r Reader[T], w Writer[T], p Projector[T]) (*Handlers, error) {
	if r == nil {
		return nil, fmt.Errorf("resource has no Reader")
	}
	if p.Row == nil {
		return nil, fmt.Errorf("projector has no Row function")
	}

	h := &Handlers{
		List: func(ctx context.Context, q Query) (RowPage, error) {
			page, err := r.List(ctx, q)
			if err != nil {
				return RowPage{}, err
			}
			rows := make([]Row, 0, len(page.Items))
			for _, item := range page.Items {
				rows = append(rows, p.Row(item))
			}
			return RowPage{Rows: rows, NextCursor: page.NextCursor}, nil
		},
		Get: func(ctx context.Context, id string) (Row, error) {
			item, err := r.Get(ctx, id)
			if err != nil {
				return Row{}, err
			}
			return p.Row(item), nil
		},
	}

	if w == nil {
		return h, nil
	}

	if p.Form == nil {
		return nil, fmt.Errorf("projector has a Writer but no Form function")
	}
	if p.Bind == nil {
		return nil, fmt.Errorf("projector has a Writer but no Bind function")
	}

	h.Form = func(ctx context.Context, id string) (map[string]string, error) {
		item, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return p.Form(item), nil
	}
	h.Create = func(ctx context.Context, v Values) (string, FieldErrors, error) {
		item, errs := p.Bind(v)
		if errs.Any() {
			// Return before touching the port. A validation failure
			// that has already written half a record is the failure
			// mode the conformance suite asserts against.
			return "", errs, nil
		}
		id, err := w.Create(ctx, item)
		if errs := faultErrors(err); errs != nil {
			return "", errs, nil
		}
		return id, nil, err
	}
	h.Update = func(ctx context.Context, id string, v Values) (FieldErrors, error) {
		item, errs := p.Bind(v)
		if errs.Any() {
			return errs, nil
		}
		err := w.Update(ctx, id, item)
		if errs := faultErrors(err); errs != nil {
			return errs, nil
		}
		return nil, err
	}
	h.Delete = w.Delete

	return h, nil
}
