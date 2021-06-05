package parallel

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ExampleFunc_simple() {
	// we want to transform this list in parallel
	input := []int{10, 20, 30, 40, 50}
	count := len(input)

	// reserve enough space for the results
	results := make([]int, len(input))

	// this is the function we'll run.
	// idx goes from 0 to count.
	fn := Func(func(idx int) error {
		// no mutex needed because every goroutine gets a unique idx, so writes never overlap
		results[idx] = input[idx] * 10
		return nil
	})

	// run fn count times in parallel. Run at most 5 at a time (concurrently).
	if err := fn.Do(count, 5); err != nil {
		panic(err)
	}

	fmt.Printf("%#v\n", results)

	// Output: []int{100, 200, 300, 400, 500}
}

func ExampleFuncs() {
	// zero value is usable
	var ops Funcs

	// just here as an example to show that both funcs run
	op1Ran := false
	ops.Add(func(idx int) error {
		op1Ran = true
		// ... do some work here
		return nil
	})

	op2Ran := false
	ops.Add(func(idx int) error {
		op2Ran = true
		// ... do some work here
		return nil
	})

	// run the operations we added. Runs at most GOMAXPROCS in parallel
	if err := ops.Do(); err != nil {
		panic(err)
	}

	fmt.Println("done! Ops ran:", op1Ran, op2Ran)

	// Output: done! Ops ran: true true
}

// TestFunc_Do tests that the right number of funcs are run, and that the maximum concurrency
// parameter is honored
func TestFunc_Do(t *testing.T) {
	t.Parallel()
	const N = 100
	adder := struct {
		Semaphore
		count int
	}{
		make(Semaphore, 1),
		0,
	}

	fn := Func(func(idx int) error {
		ok, release := adder.MaybeAcquire()
		defer release()
		if !ok {
			return fmt.Errorf("goroutine %d running in parallel with another one", idx)
		}
		adder.count += 1
		time.Sleep(100 * time.Microsecond)
		return nil
	})

	require.NoError(t, fn.Do(N, 1), "unexpected error from Do")

	assert.EqualValues(t, N, adder.count, "all operations didn't run")
}

// TestFuncs_Do tests that all given funcs are run, and that the maximum concurrency parameter
// is honored
func TestFuncs_Do(t *testing.T) {
	t.Parallel()
	const N = 100
	adder := struct {
		Semaphore
		count int
	}{
		make(Semaphore, 1),
		0,
	}

	var ops Funcs

	for i := 0; i < N; i++ {
		ops.Add(func(idx int) error {
			ok, release := adder.MaybeAcquire()
			defer release()
			if !ok {
				return fmt.Errorf("goroutine %d running in parallel with another one", idx)
			}
			adder.count += 1
			time.Sleep(100 * time.Microsecond)
			return nil
		})
	}

	require.NoError(t, ops.Do(1), "unexpected error from Do")
	assert.EqualValues(t, N, adder.count, "all funcs didn't run")
}

func TestErrors_Add(t *testing.T) {
	const N = 10
	pe := new(Errors)
	require.NoError(t, Func(func(idx int) error {
		pe.Add(fmt.Errorf("%d", idx))
		return nil
	}).Do(N), "unexpected error from Do")

	if pe.errs == nil {
		t.Fatalf("expected non-nil errs")
	}
	if len(*pe.errs) != N {
		t.Errorf("expected len %d, got %d", N, len(*pe.errs))
	}
}
