// Package parallel provides tooling for running a bounded number of functions in parallel and
// waiting for them to finish.
//
// Returning results from goroutines
//
// The Func type's examples show how you can return results from goroutines without having to use
// channels or locking. The general idea is to pre-reserve a slice for them and have each goroutine
// write into its own index in the slice. This means that you're possibly using more memory, but
// depending on how many things you're actually doing in parallel and what sort of results you're
// returning, this method will actually perform better than more complex solutions.
//
//	 	// reserve enough space for the results
//		results := make([]resultType, len(input))
//
//		// this is the function we want to parallelize.
//		// idx will go from 0 to len(input) (see Do call below)
//		fn := Func(func(idx int) error {
//			// no mutex needed because every goroutine gets a unique idx, so writes never overlap
//			results[idx], err := doSomethingCPUIntensiveTo(input[idx])
//
//			return err
//		})
//
//		// run fn in parallel len(input) times, with at most GOMAXPROCS at a time.
//		err := fn.Do(len(input))
//
//		for i, r := range results {
//			// do something to all the results
//		}
package parallel

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"go.uber.org/multierr"
)

// Func is a parallelizable function. Each function running in parallel with others is given a
// unique index number, starting from 0
type Func func(idx int) error

// Do runs fn count times and in parallel. All are run even if one or more returns an error. The idx
// parameter for fn goes from 0 to count.
//
// If a maxParallel is given, only that many are run concurrently. Defaults to GOMAXPROCS.
func (fn Func) Do(count int, maxParallel ...int) (err error) {
	if count == 0 {
		return
	}

	max := maxproc
	if len(maxParallel) != 0 && maxParallel[0] > 0 {
		max = maxParallel[0]
	}

	var wg sync.WaitGroup
	wg.Add(count)
	var errs Errors
	sema := NewSemaphore(max)
	for i := 0; i < count; i++ {
		go fn.doOne(i, &wg, sema, &errs)
	}
	wg.Wait()
	return errs.Err()
}

func (fn Func) doOne(i int, wg *sync.WaitGroup, sema Semaphore, errs *Errors) {
	defer wg.Done()
	defer sema.AcquireRelease()
	errs.Add(fn(i))
}

// Funcs allows running multiple different Funcs in parallel. Use Add to add a Func to be run,
// and Do to run them. The zero value of Funcs is usable
type Funcs []Func

// Add a new Func to ops
func (fns *Funcs) Add(op Func) {
	*fns = append(*fns, op)
}

// Do runs all the Funcs in ops. Each Func gets its index number in fns as an argument.
//
// Do is not idempotent: all Funcs run on each call.
//
// If a maxParallel is given, only that many Funcs execure concurrently. Defaults to
// GOMAXPROCS.
func (fns Funcs) Do(maxParallel ...int) error {
	if len(fns) == 0 {
		return nil
	}
	var wg sync.WaitGroup

	numFns := len(fns)
	wg.Add(numFns)

	var errs Errors

	max := maxproc
	if len(maxParallel) != 0 && maxParallel[0] > 0 {
		max = maxParallel[0]
	}

	sema := NewSemaphore(max)

	for i, fn := range fns {
		go doOp(i, &wg, fn, sema, &errs)
	}

	wg.Wait()

	return errs.Err()
}

func doOp(i int, wg *sync.WaitGroup, fn Func, sema Semaphore, errs *Errors) {
	defer wg.Done()
	defer sema.AcquireRelease()()
	errs.Add(fn(i))
}

// Errors is for gathering errors in a thread-safe manner. The zero value is usable.
//
// Lock-free. If no errors are added, Errors uses a pointer's worth of memory.
type Errors struct {
	errs *[]error
}

// Add err to p. Thread-safe.
func (p *Errors) Add(err error) {
	if err == nil {
		return
	}

	// essentially **[]error
	pointerToP := (*unsafe.Pointer)(unsafe.Pointer(&p.errs))

	// make sure that p.errs is always initialized by trying to swap a nil *[]error for new([]error)
	_ = atomic.CompareAndSwapPointer(
		pointerToP,
		unsafe.Pointer((*[]error)(nil)),
		unsafe.Pointer(new([]error)))

	// we return here if the CAS fails
retry:
	// load current value
	current := (*[]error)(atomic.LoadPointer(pointerToP))
	// create a new slice and then append current into it, instead of appending to current.
	// This means that current itself is never modified, making this thread-safe
	newVal := append(append(make([]error, 0, len(*current)+1), err), *current...)

	// Try to swap the new list to p.errs
	ok := atomic.CompareAndSwapPointer(
		pointerToP,
		unsafe.Pointer(current),
		unsafe.Pointer(&newVal))
	if !ok {
		// damn, someone beat us to it. Let's try again
		goto retry
	}
	return
}

// Err returns a multierror (go.uber.org/multierr) of errors added to p, or nil if none were added.
// NOTE: not thread-safe.
func (p Errors) Err() error {
	if p.errs == nil {
		return nil
	}
	return multierr.Combine(*p.errs...)
}

// List returns errors added to p, if any.
// NOTE: not thread-safe.
func (p Errors) List() []error {
	if p.errs == nil {
		return nil
	}
	return *p.errs
}

// Semaphore is a counting semaphore. It allows limiting concurrency.
//
// 		s := NewSemaphore(10)
//		// we want only 10 of these to run in parallel
//		fn := go func() {
//			// Calls s.AcquireRelease() which acquires the semaphore, and then defers the release
//			// func returned by AcquireRelease.
//			defer s.AcquireRelease()()
//			// ... do stuff ...
//		}
//		// start a bunch of fn
type Semaphore chan struct{}

// NewSemaphore creates a new Semaphore with a maximum count of max.
func NewSemaphore(max int) Semaphore {
	if max < 1 {
		panic(fmt.Errorf("NewSemaphore called with max < 1: %d", max))
	}
	return make(Semaphore, max)
}

// Acquire the semaphore. Blocks until available. See also AcquireRelease
func (s Semaphore) Acquire() {
	s <- struct{}{}
}

// AcquireRelease acquires the semaphore and returns a func that can be called to release it. Blocks
// until available. Intended to be used with defer:
//
//		// Calls s.AcquireRelease() which acquires the semaphore, and then defers the release
//		// func returned by AcquireRelease.
// 		defer s.AcquireRelease()()
func (s Semaphore) AcquireRelease() (release func()) {
	s <- struct{}{}
	return s.Release
}

// MaybeAcquire is like AcquireRelease but it doesn't block. ok is false if the semaphore couldn't
// be acquired. The release func is a no-op if ok was false, so it's always safe to call:
//
//		ok, release := adder.MaybeAcquire()
//		defer release()
//		if !ok {
//			// do stuff
//		}
func (s Semaphore) MaybeAcquire() (ok bool, release func()) {
	release = noop
	select {
	case s <- struct{}{}:
		ok = true
		release = s.Release
	default:
	}
	return
}

// Release the semaphore. See also AcquireRelease
func (s Semaphore) Release() {
	<-s
}

func noop() {}

var maxproc = runtime.GOMAXPROCS(0)
