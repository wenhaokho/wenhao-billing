package schedule_test

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// The repo has no assertion library; this is the small slice of one the
// schedule tests use. require stops the test on failure, assert carries on.
type checker struct{ fatal bool }

var (
	assert  = checker{}
	require = checker{fatal: true}
)

func (c checker) fail(t *testing.T, format string, args ...any) {
	t.Helper()
	if c.fatal {
		t.Fatalf(format, args...)
	}
	t.Errorf(format, args...)
}

func (c checker) Equal(t *testing.T, want, got any, _ ...any) {
	t.Helper()
	if wt, ok := want.(time.Time); ok {
		if gt, ok := got.(time.Time); ok && wt.Equal(gt) {
			return
		}
	} else if reflect.DeepEqual(want, got) {
		return
	}
	c.fail(t, "want %v, got %v", want, got)
}

func (c checker) True(t *testing.T, got bool, msg ...any) {
	t.Helper()
	if !got {
		c.fail(t, "want true %v", msg)
	}
}

func (c checker) False(t *testing.T, got bool, msg ...any) {
	t.Helper()
	if got {
		c.fail(t, "want false %v", msg)
	}
}

func (c checker) Nil(t *testing.T, got any) {
	t.Helper()
	if v := reflect.ValueOf(got); got != nil && (v.Kind() != reflect.Pointer || !v.IsNil()) {
		c.fail(t, "want nil, got %v", got)
	}
}

func (c checker) NotNil(t *testing.T, got any) {
	t.Helper()
	if v := reflect.ValueOf(got); got == nil || (v.Kind() == reflect.Pointer && v.IsNil()) {
		c.fail(t, "want not nil")
	}
}

func (c checker) NoError(t *testing.T, err error, msg ...any) {
	t.Helper()
	if err != nil {
		c.fail(t, "unexpected error %v %v", err, msg)
	}
}

func (c checker) Error(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		c.fail(t, "want an error")
	}
}

func (c checker) ErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		c.fail(t, "error %v does not match %v", err, target)
	}
}
